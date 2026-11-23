// Package display creates virtual displays by starting scrcpy-server.
//
// Android 13 on this ROM has no `cmd display create` ("Unknown command: create"),
// so the only way to get an overlay display is scrcpy-server's own
// `new_display=WxH/DPI` mode: the server creates the display, mirrors it over an
// abstract socket, and destroys it when it exits. The display id is therefore
// owned by the scrcpy process, not by a framework command — which is why this
// package keeps the process handle instead of just returning an id.
package display

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Process is one virtual display backed by a running scrcpy-server.
type Process struct {
	ID      int
	Name    string
	Width   int
	Height  int
	DPI     int
	Socket  string
	Created time.Time

	cmd      *exec.Cmd
	log      string
	conn     net.Conn
	listener net.Listener

	mu          sync.Mutex
	subs        map[chan Frame]struct{}
	spsPPS      []byte
	lastErr     string
	frames      int64
	bytes       int64
	lastPTS     int64
	lastFrameAt time.Time
}

var (
	mu       sync.Mutex
	displays = map[int]*Process{}
)

// Manager starts and stops overlay displays.
type Manager struct {
	Jar     string // absolute path to scrcpy-server.jar
	DataDir string // for logs
	Scid    func() int
}

// scrcpy prints: "New display: 720x1280/320 (id=2)". The id is the parenthesised
// one; matching the width instead produces a display id of 720, which then fails
// every later capture (verified against this device).
var idRE = regexp.MustCompile(`\(id=(\d+)\)`)

// EnsureMirror starts scrcpy-server mirroring an EXISTING display (the phone's
// own screen, display 0, or a virtual display created elsewhere).
//
// Mirror mode has no "New display" line to read an id from, so the caller states
// which display it is; the frame size still comes from the stream header, which is
// the only trustworthy source for it.
func (m Manager) EnsureMirror(ctx context.Context, display int) (*Process, error) {
	if p, ok := Get(display); ok {
		// A mirror whose parser died must be replaced, not handed out again: the
		// socket is still open but nothing will ever arrive on it, so every viewer
		// would wait for frames that cannot come (observed after a stream-desync:
		// "invalid packet size 785956032", then 0 packets for the rest of the
		// process's life). Restarting is the only way back.
		if p.Err() == nil {
			return p, nil
		}
		m.Drop(display)
	}
	return m.start(ctx, "", "", 0, 0, 0, display)
}

// Create starts scrcpy-server in new_display mode and returns the overlay display
// it created.
//
// The display id is discovered from the server's own output, not guessed: scrcpy
// prints the display it added, and a wrong id would make every later capture
// target the physical screen.
// Drop stops a mirror and forgets it, so the next EnsureMirror starts clean.
func (m Manager) Drop(display int) {
	p, ok := Get(display)
	if !ok {
		return
	}
	p.Stop()
	mu.Lock()
	delete(displays, display)
	mu.Unlock()
}

func (m Manager) Create(ctx context.Context, name string, width, height, dpi int) (*Process, error) {
	if width <= 0 || height <= 0 || dpi <= 0 {
		return nil, fmt.Errorf("width, height and dpi must be positive")
	}
	if name == "" {
		name = "sextant-vd"
	}
	return m.start(ctx, name, fmt.Sprintf("%dx%d/%d", width, height, dpi), width, height, dpi, 0)
}

// start launches scrcpy-server either in new_display mode (newDisplay != "") or
// mirroring mirrorDisplay.
func (m Manager) start(ctx context.Context, name, newDisplay string, width, height, dpi, mirrorDisplay int) (*Process, error) {
	if _, err := os.Stat(m.Jar); err != nil {
		return nil, fmt.Errorf("scrcpy-server jar missing: %w", err)
	}
	scid := 0x10000000
	if m.Scid != nil {
		scid = m.Scid()
	}
	// scrcpy-server connects to the abstract socket named `scrcpy_<scid>`
	// (Server.java: DesktopConnection.open("scrcpy_" + scid)). Any other name and
	// it dies with "Connection refused" before creating the display.
	socket := fmt.Sprintf("@scrcpy_%08x", scid)

	appProcess := "/system/bin/app_process64"
	if _, err := os.Stat(appProcess); err != nil {
		appProcess = "/system/bin/app_process"
	}
	args := []string{
		"/system/bin",
		"com.genymobile.scrcpy.Server",
		"4.1",
		"log_level=INFO",
		"audio=false",
		"control=false",
		"video=true",
		"video_codec=h264",
		"video_source=display",
		"tunnel_forward=false",
		fmt.Sprintf("scid=%08x", scid),
		"send_device_meta=false",
		"send_frame_meta=true",
		"send_stream_meta=true",
		"send_dummy_byte=false",
		"cleanup=true",
		"power_on=false",
		"stay_awake=false",
		"max_fps=30",
	}
	if newDisplay != "" {
		args = append(args, "new_display="+newDisplay)
	} else {
		args = append(args, fmt.Sprintf("display_id=%d", mirrorDisplay))
	}
	// scrcpy-server CONNECTS to a socket we must be listening on first; without a
	// listener it dies with "Connection refused" before it ever creates the
	// display. The connection is kept open and drained for the life of the
	// display: closing it makes the server tear the display down.
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", socket, err)
	}
	cmd := exec.Command(appProcess, args...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLASSPATH=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "CLASSPATH="+m.Jar)
	logPath := filepath.Join(m.DataDir, "log", fmt.Sprintf("scrcpy-%08x.log", scid))
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = lf
	cmd.Stderr = lf
	if err := cmd.Start(); err != nil {
		_ = ln.Close()
		_ = lf.Close()
		return nil, fmt.Errorf("start scrcpy-server: %w", err)
	}
	connCh := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(connCh)
			return
		}
		connCh <- conn
	}()

	p := &Process{
		Name: name, Width: width, Height: height, DPI: dpi,
		Socket: socket, Created: time.Now(), cmd: cmd, log: logPath,
		subs: map[chan Frame]struct{}{},
	}
	// Wait for the server to connect before reading the log: it reports the new
	// display id right after the handshake.
	var conn net.Conn
	select {
	case c, ok := <-connCh:
		if !ok {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			_ = ln.Close()
			_ = lf.Close()
			return nil, fmt.Errorf("scrcpy-server did not connect to %s", socket)
		}
		conn = c
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = ln.Close()
		_ = lf.Close()
		return nil, fmt.Errorf("scrcpy-server did not connect to %s within 8s (see %s)", socket, logPath)
	}
	id := mirrorDisplay
	if newDisplay != "" {
		id, err = waitForDisplayID(ctx, logPath, 8*time.Second)
	}
	if err != nil {
		_ = conn.Close()
		_ = ln.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = lf.Close()
		return nil, err
	}
	p.ID = id
	p.conn = conn
	p.listener = ln
	// Read the codec header now: it carries the real frame size, which is what a
	// viewer needs before it can even create a decoder.
	if err := p.ReadHeader(); err != nil {
		_ = conn.Close()
		_ = ln.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = lf.Close()
		return nil, fmt.Errorf("scrcpy stream header: %w", err)
	}
	p.StartReading()
	go func() {
		_ = cmd.Wait()
		_ = lf.Close()
		mu.Lock()
		delete(displays, p.ID)
		mu.Unlock()
	}()
	mu.Lock()
	displays[p.ID] = p
	mu.Unlock()
	return p, nil
}

// waitForDisplayID tails the scrcpy log until it reports a new display.
func waitForDisplayID(ctx context.Context, logPath string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if id := scanDisplayID(logPath); id > 0 {
			return id, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return 0, fmt.Errorf("scrcpy-server started but never reported a display id (see %s)", logPath)
}

func scanDisplayID(logPath string) int {
	f, err := os.Open(logPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(strings.ToLower(line), "display") {
			continue
		}
		if m := idRE.FindStringSubmatch(line); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// List returns the live overlay displays.
func List() []*Process {
	mu.Lock()
	defer mu.Unlock()
	out := make([]*Process, 0, len(displays))
	for _, p := range displays {
		out = append(out, p)
	}
	return out
}

// Get returns one overlay display.
func Get(id int) (*Process, bool) {
	mu.Lock()
	defer mu.Unlock()
	p, ok := displays[id]
	return p, ok
}

// Destroy stops the scrcpy-server that owns the display; the display goes away
// with it because cleanup=true.
func (p *Process) Destroy() error {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// LogPath is where this display's scrcpy output went.
func (p *Process) LogPath() string { return p.log }
