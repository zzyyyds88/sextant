// Package helper talks to the root app_process helper.
//
// The helper is the only way to reach the framework from a root process: it
// holds the UiAutomation session, the notification listener, the SMS/telephony
// bridge, the ROM OCR engine and the location client. The daemon loads it as
// /system/bin/app_process64 + lib/helper.jar and speaks JSON over an abstract
// Unix socket, guarded by a token file only root can read.
//
// One constraint belongs here rather than to any caller: the helper registers
// UiAutomation with FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES(1). Without it,
// running UiAutomation silently unbinds every third-party accessibility service
// on the device while settings still look correct — which is exactly why the old
// keep-alive loop could never repair anything.
package helper

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Client is a lazy, restarting helper connection.
type Client struct {
	DataDir string
	Jar     string // absolute path to helper.jar
	Socket  string // abstract socket name, with or without '@'
	Token   string // absolute path to the token file

	mu      sync.Mutex
	proc    *exec.Cmd
	log     func(format string, args ...any)
	onCall  func(err error, ms int64)
	subs    map[chan Event]struct{}
	subOnce sync.Once
	subStop chan struct{}

	// eventMu/eventRefs keep the helper's accessibility stream alive while any
	// caller still needs it. A single SSE client disconnecting used to stop the
	// stream globally and silently downgrade every future wait to polling.
	eventMu   sync.Mutex
	eventRefs int
}

// Event is one pushed frame from the helper.
//
// The helper pushes notifications, accessibility events and broadcast matches over
// a single subscription socket; that is why subscribers see an Event with a name
// rather than a typed payload per feature.
type Event struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// Options configures a Client.
type Options struct {
	DataDir string
	Jar     string
	Socket  string
	Token   string
	Log     func(format string, args ...any)
	// OnCall, when set, receives every completed RPC's duration and error. The
	// daemon uses it for the D33 metrics; tests leave it nil.
	OnCall func(err error, ms int64)
}

// New builds a helper client.
func New(o Options) *Client {
	if o.Socket == "" {
		o.Socket = "@chopin-core-helper"
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	return &Client{
		DataDir: o.DataDir, Jar: o.Jar, Socket: o.Socket, Token: o.Token, log: o.Log,
		onCall: o.OnCall,
		subs:   map[chan Event]struct{}{}, subStop: make(chan struct{}),
	}
}

func (c *Client) tokenPath() string {
	if c.Token != "" {
		return c.Token
	}
	return filepath.Join(c.DataDir, "state", "helper.token")
}

func (c *Client) socketAddr() string {
	name := strings.TrimPrefix(c.Socket, "@")
	return "@" + name
}

// Ensure starts the helper when it is not already listening.
func (c *Client) Ensure(ctx context.Context) error {
	if c.Available(ctx) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Available(ctx) {
		return nil
	}
	if _, err := os.Stat(c.Jar); err != nil {
		return fmt.Errorf("helper jar missing: %w", err)
	}
	if err := c.writeToken(); err != nil {
		return err
	}
	logPath := filepath.Join(c.DataDir, "log", "helper.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("helper log: %w", err)
	}
	appProcess := "/system/bin/app_process64"
	if _, statErr := os.Stat(appProcess); statErr != nil {
		appProcess = "/system/bin/app_process"
	}
	cmd := exec.Command(appProcess, "/system/bin", "com.chopin.core.helper.HelperMain")
	// AndroidRuntime reads CLASSPATH through getenv(); exactly one value.
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLASSPATH=") || strings.HasPrefix(kv, "CHOPIN_HELPER_") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env,
		"CLASSPATH="+c.Jar,
		"CHOPIN_HELPER_TOKEN_FILE="+c.tokenPath(),
		"CHOPIN_HELPER_SOCKET="+strings.TrimPrefix(c.Socket, "@"),
		"CHOPIN_OCR_CACHE="+filepath.Join(c.DataDir, "cache", "ocr"),
	)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return fmt.Errorf("start helper: %w", err)
	}
	c.proc = cmd
	go func() {
		_ = cmd.Wait()
		_ = logf.Close()
		c.log("helper exited: pid %d", cmd.Process.Pid)
	}()
	c.log("helper started: pid %d", cmd.Process.Pid)
	// Wait for the socket instead of assuming it is instantly there.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c.Available(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("helper started but its socket did not come up")
}

func (c *Client) writeToken() error {
	path := c.tokenPath()
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 16 {
		return nil
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(buf)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Available reports whether the helper answers right now.
func (c *Client) Available(ctx context.Context) bool {
	if _, err := os.Stat(c.tokenPath()); err != nil {
		return false
	}
	dctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "unix", c.socketAddr())
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

type request struct {
	ID     string         `json:"id"`
	Token  string         `json:"token"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type response struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Error is a structured helper failure.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Call performs one helper request and decodes its data into out.
func (c *Client) Call(ctx context.Context, method string, params map[string]any, out any) error {
	start := time.Now()
	err := c.call(ctx, method, params, out)
	if c.onCall != nil {
		c.onCall(err, time.Since(start).Milliseconds())
	}
	return err
}

func (c *Client) call(ctx context.Context, method string, params map[string]any, out any) error {
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	tok, err := os.ReadFile(c.tokenPath())
	if err != nil {
		return fmt.Errorf("helper token: %w", err)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socketAddr())
	if err != nil {
		return fmt.Errorf("helper socket: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(90 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	id := make([]byte, 8)
	_, _ = rand.Read(id)
	req := request{ID: hex.EncodeToString(id), Token: strings.TrimSpace(string(tok)), Method: method, Params: params}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for sc.Scan() {
		var resp response
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			continue // event frames and partial lines are not ours
		}
		if resp.ID != req.ID {
			continue
		}
		if !resp.OK {
			if resp.Error != nil {
				return &Error{Code: resp.Error.Code, Message: resp.Error.Message}
			}
			return fmt.Errorf("helper error")
		}
		if out != nil && len(resp.Data) > 0 {
			return json.Unmarshal(resp.Data, out)
		}
		return nil
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("helper closed the connection without answering")
}

// SubscribeEvents returns a channel of helper pushes plus a cancel function.
//
// One background reader feeds every subscriber so the helper sees a single
// connection: opening one subscription per API stream would multiply the helper's
// fan-out cost and leak sockets when a browser tab closes badly.
func (c *Client) SubscribeEvents() (<-chan Event, func()) {
	c.subOnce.Do(func() { go c.pump() })
	ch := make(chan Event, 64)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		if _, ok := c.subs[ch]; ok {
			delete(c.subs, ch)
			close(ch)
		}
		c.mu.Unlock()
	}
}

// pump keeps one helper subscription open and fans its frames out.
func (c *Client) pump() {
	for {
		select {
		case <-c.subStop:
			return
		default:
		}
		if err := c.pumpOnce(); err != nil {
			c.log("helper events: %v", err)
		}
		select {
		case <-c.subStop:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) pumpOnce() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	tok, err := os.ReadFile(c.tokenPath())
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socketAddr())
	if err != nil {
		return err
	}
	defer conn.Close()
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	req := request{ID: hex.EncodeToString(id), Token: strings.TrimSpace(string(tok)), Method: "subscribe"}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Event == "" {
			continue // the subscribe acknowledgement is not an event
		}
		c.mu.Lock()
		targets := make([]chan Event, 0, len(c.subs))
		for ch := range c.subs {
			targets = append(targets, ch)
		}
		c.mu.Unlock()
		for _, ch := range targets {
			select {
			case ch <- ev:
			default: // a slow consumer must not stall the pump for everyone
			}
		}
	}
	return sc.Err()
}

// Health returns the helper's own view of what it could initialize.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "health", nil, &out)
	return out, err
}

// OCR runs the ROM OCR engine on the current screen (or a region).
func (c *Client) OCR(ctx context.Context, display int, region map[string]int) (map[string]any, error) {
	var out map[string]any
	params := map[string]any{"display_id": display}
	if region != nil {
		params["region"] = region
	}
	err := c.Call(ctx, "ocr", params, &out)
	return out, err
}

// Screenshot captures display 0 through UiAutomation.
//
// This is the fast path for agent perception: `screencap -p` spends most of its
// time PNG-encoding on the device CPU, while the helper can hand back a JPEG
// (or write it to a file) from the frame it already holds for OCR and the tree.
func (c *Client) Screenshot(ctx context.Context, params map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "screenshot", params, &out)
	return out, err
}

// Observe captures one frame and performs every observation the caller selected
// on it (tree, OCR, pixels, encoded frame). Keeping them together is the point:
// a second capture would cost more than the observations themselves.
func (c *Client) Observe(ctx context.Context, params map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "observe", params, &out)
	return out, err
}

// OCRPNG runs ROM OCR on a caller-supplied PNG (virtual displays).
func (c *Client) OCRPNG(ctx context.Context, display int, png []byte, region map[string]int) (map[string]any, error) {
	var out map[string]any
	params := map[string]any{"display_id": display, "png_base64": encodeBase64(png)}
	if region != nil {
		params["region"] = region
	}
	err := c.Call(ctx, "ocr_png", params, &out)
	return out, err
}

// Notifications returns the current notification list.
func (c *Client) Notifications(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "notifications", nil, &out)
	return out, err
}

// DismissNotification dismisses one notification by key.
func (c *Client) DismissNotification(ctx context.Context, key string) error {
	return c.Call(ctx, "notification_dismiss", map[string]any{"key": key}, nil)
}

// SMS lists messages.
func (c *Client) SMSList(ctx context.Context, limit int) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "sms_list", map[string]any{"limit": limit}, &out)
	return out, err
}

// SMSSend sends a message; the helper reports a message id to poll.
func (c *Client) SMSSend(ctx context.Context, to, text string) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "sms_send", map[string]any{"to": to, "text": text}, &out)
	return out, err
}

// SMSStatus polls a send result.
func (c *Client) SMSStatus(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "sms_status", map[string]any{"id": id}, &out)
	return out, err
}

// Location reads the current location.
func (c *Client) Location(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "location", nil, &out)
	return out, err
}

// KeepAwake holds or releases a partial wake lock in the helper process.
func (c *Client) KeepAwake(ctx context.Context, enabled bool, timeoutMS int) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "keep_awake", map[string]any{
		"enabled": enabled, "timeout_ms": timeoutMS,
	}, &out)
	return out, err
}

// Windows returns the accessibility window inventory (display → windows).
func (c *Client) Windows(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "a11y_windows", nil, &out)
	return out, err
}

// UITree reads the accessibility tree through UiAutomation.
func (c *Client) UITree(ctx context.Context, display int) (map[string]any, error) {
	var out map[string]any
	err := c.Call(ctx, "a11y_tree", map[string]any{"display_id": display}, &out)
	return out, err
}

// EventsStart/Stop toggle real-time accessibility event broadcasting.
func (c *Client) EventsStart(ctx context.Context) error {
	return c.Call(ctx, "a11y_events_start", nil, nil)
}

// RetainEvents starts the event stream on the first caller and keeps it alive
// while any caller holds a reference. The companion ReleaseEvents must be
// called exactly once for each successful retain.
func (c *Client) RetainEvents(ctx context.Context) error {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventRefs > 0 {
		c.eventRefs++
		return nil
	}
	if err := c.EventsStart(ctx); err != nil {
		return err
	}
	c.eventRefs = 1
	return nil
}

// ReleaseEvents drops one reference; the last one stops the stream.
func (c *Client) ReleaseEvents() {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventRefs == 0 {
		return
	}
	c.eventRefs--
	if c.eventRefs > 0 {
		return
	}
	_ = c.EventsStop(context.Background())
}

// StopAutomation releases the helper's UiAutomation session.
//
// UiAutomation is a single global resource: while the helper holds it, any
// `uiautomator dump` started elsewhere is killed ("signal: killed"), which is
// exactly what happened to the unlock path. Callers that need the dump must ask
// the helper to let go first, then stop asking and let it re-arm on next use.
func (c *Client) StopAutomation(ctx context.Context) error {
	return c.Call(ctx, "ui_automation_stop", nil, nil)
}

// EventsStop stops the event stream.
func (c *Client) EventsStop(ctx context.Context) error {
	return c.Call(ctx, "a11y_events_stop", nil, nil)
}

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// SetTextViaA11y types into a field through ACTION_SET_TEXT, without touching the
// device's keyboard.
//
// It is the fast, polite path: one helper call (~50 ms) instead of `ime set` + a
// commit + `ime set` back (~1.6 s), and the person's keyboard never moves. The
// module IME stays for fields that refuse ACTION_SET_TEXT.
func (c *Client) SetTextViaA11y(ctx context.Context, text, resourceID string, clear bool, display, x, y int) (map[string]any, error) {
	params := map[string]any{
		"text": text, "rid": resourceID, "clear": clear, "display_id": display,
	}
	if x >= 0 && y >= 0 {
		params["x"], params["y"] = x, y
	}
	var out map[string]any
	if err := c.Call(ctx, "a11y_set_text", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ScrollViaA11y scrolls the list that contains (x, y) through the node's own scroll
// action, so the finger position cannot decide which thing moves.
//
// It returns the route that worked ("node") or "none" when the helper found no
// scrollable node — which is normal for app-drawn lists (WeChat's chat view) and is
// exactly when the caller must fall back to a gesture.
func (c *Client) ScrollViaA11y(ctx context.Context, direction string, display, x, y int) (map[string]any, error) {
	params := map[string]any{"direction": direction, "display_id": display}
	if x >= 0 && y >= 0 {
		params["x"], params["y"] = x, y
	}
	var out map[string]any
	if err := c.Call(ctx, "a11y_scroll", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}
