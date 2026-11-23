package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chopin-core/internal/httpx"
)

// App management.
//
// Installs arrive as an upload: the body is written to tmp/, then `pm install`
// runs on the file. Uninstall / clear-data / reboot are the three destructive
// operations and require the agent confirmation handshake (DESIGN D13).

// confirmation is a one-time token for a destructive action.
type confirmation struct {
	Action    string
	Params    map[string]string
	CreatedAt time.Time
	Expires   time.Time
	Used      bool
}

var (
	confirmMu sync.Mutex
	confirms  = map[string]*confirmation{}
)

const confirmTTL = 5 * time.Minute

func newConfirmation(action string, params map[string]string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	confirmMu.Lock()
	defer confirmMu.Unlock()
	// Keep the map bounded: drop expired entries on every mint.
	now := time.Now()
	for k, v := range confirms {
		if now.After(v.Expires) || v.Used {
			delete(confirms, k)
		}
	}
	confirms[id] = &confirmation{Action: action, Params: params, CreatedAt: now, Expires: now.Add(confirmTTL)}
	return id
}

func takeConfirmation(id, action string, params map[string]string) error {
	confirmMu.Lock()
	defer confirmMu.Unlock()
	c, ok := confirms[id]
	if !ok {
		return fmt.Errorf("unknown confirmation %q", id)
	}
	if c.Used {
		return fmt.Errorf("confirmation %q was already used", id)
	}
	if time.Now().After(c.Expires) {
		delete(confirms, id)
		return fmt.Errorf("confirmation %q expired", id)
	}
	if c.Action != action {
		return fmt.Errorf("confirmation %q was issued for %q, not %q", id, c.Action, action)
	}
	for k, v := range params {
		if c.Params[k] != v {
			return fmt.Errorf("confirmation %q does not match parameter %q", id, k)
		}
	}
	c.Used = true
	delete(confirms, id)
	return nil
}

// handleApps lists packages.
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	out, err := s.Probe.Run.Run(r.Context(), "pm", "list", "packages", "-3")
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "pm list failed", err)
		return
	}
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package:") {
			pkgs = append(pkgs, strings.TrimPrefix(line, "package:"))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"count": len(pkgs), "packages": pkgs})
}

// handleForeground reports the package on screen.
func (s *Server) handleForeground(w http.ResponseWriter, r *http.Request) {
	out, err := s.Probe.Run.Run(r.Context(), "dumpsys", "window")
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "dumpsys window failed", err)
		return
	}
	// mCurrentFocus first, then mFocusedApp.
	//
	// They are not the same thing: during a transition mCurrentFocus can be absent
	// while mFocusedApp still names the activity, and a single sample then answered
	// "no focused window" for a phone that plainly had Settings in front. Reading the
	// second line before giving up is what makes this reliable right after a launch.
	for _, key := range []string{"mCurrentFocus=", "mFocusedApp="} {
		if fg := focusedFrom(out, key); fg != nil {
			httpx.WriteJSON(w, http.StatusOK, fg)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"package": "", "component": "",
		"detail": "no focused window (device may be on the lock screen or the panel is off)"})
}

// focusedFrom extracts the component from the first line carrying key.
func focusedFrom(out, key string) map[string]any {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, key) {
			continue
		}
		// mCurrentFocus=Window{abc u0 com.pkg/com.pkg.Activity}
		i := strings.Index(line, " u0 ")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+4:])
		rest = strings.TrimSuffix(rest, "}")
		// mFocusedApp=ActivityRecord{hash u0 com.pkg/.Act} t836} — the trailing task id
		// is not part of the component.
		if j := strings.Index(rest, "}"); j > 0 {
			rest = rest[:j]
		}
		rest = strings.TrimSpace(rest)
		pkg := rest
		if j := strings.Index(rest, "/"); j > 0 {
			pkg = rest[:j]
		}
		if !strings.Contains(rest, "/") {
			continue
		}
		return map[string]any{
			"package": pkg, "component": rest, "raw": strings.TrimSpace(line),
		}
	}
	return nil
}

// handleLaunch starts an app by package or component.
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Package   string `json:"package"`
		Component string `json:"component"`
		// ClearTask gives a task a deterministic starting screen. Android's normal
		// launch resumes the previous task stack, which is why `global home` +
		// `launch` kept returning to a page the agent had already left.
		ClearTask bool `json:"clear_task"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Package == "" && body.Component == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "package or component is required", nil)
		return
	}
	component := body.Component
	if component == "" {
		// `monkey -p <pkg>` is the obvious way to launch by package and it does not
		// work here: on this ROM /system/bin/monkey is a shell script with no
		// shebang, so exec() fails with "exec format error" (an `adb shell` hides
		// that by running it through sh). Resolve the launcher activity instead and
		// start it by component, which is one exec of a real binary.
		out, err := s.Probe.Run.Run(r.Context(), "cmd", "package", "resolve-activity",
			"--brief", "-c", "android.intent.category.LAUNCHER", body.Package)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot resolve the launcher activity", err)
			return
		}
		component = lastComponentLine(out)
		if component == "" {
			httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound,
				"no launcher activity for "+body.Package, nil)
			return
		}
	}
	startArgs := []string{"start", "-n", component}
	if body.ClearTask {
		// FLAG_ACTIVITY_NEW_TASK | FLAG_ACTIVITY_CLEAR_TASK
		startArgs = append(startArgs, "-f", "0x10008000")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if _, err := s.Probe.Run.Run(ctx, "am", startArgs...); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "launch failed", err)
		return
	}
	// PhoneGo's device.launch returns "did the target reach the front", because
	// `am start` reports success even when the ROM hands the intent to another
	// app. HyperOS answers some actions with com.android.phone's page, so the
	// only trustworthy signal is the foreground package afterwards.
	target := body.Package
	if target == "" {
		target = strings.SplitN(component, "/", 2)[0]
	}
	landed, foreground := s.waitForeground(ctx, target, 6*time.Second)
	if !landed {
		s.Audit.Warn("apps: launch %s did not land (foreground %q)", component, foreground)
		httpx.WriteError(w, http.StatusServiceUnavailable, "launch_not_landed",
			"the app did not come to the front", fmt.Errorf("foreground is %q, want %q", foreground, target))
		return
	}
	s.Audit.Info("apps: launch %s landed (foreground %s)", component, foreground)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "landed": true, "package": target, "foreground": foreground,
	})
}

// waitForeground polls the focused window until pkg owns it.
func (s *Server) waitForeground(ctx context.Context, pkg string, budget time.Duration) (bool, string) {
	deadline := time.Now().Add(budget)
	last := ""
	for {
		if fg, err := s.foregroundInfo(ctx); err == nil {
			if name, _ := fg["package"].(string); name != "" {
				last = name
				if name == pkg {
					return true, last
				}
			}
		}
		if time.Now().After(deadline) {
			return false, last
		}
		select {
		case <-ctx.Done():
			return false, last
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// handleStop force-stops an app.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Package string `json:"package"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Package == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "package is required", nil)
		return
	}
	if _, err := s.Probe.Run.Run(r.Context(), "am", "force-stop", body.Package); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "force-stop failed", err)
		return
	}
	s.Audit.Info("apps: force-stop %s", body.Package)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeepLink opens a URI.
func (s *Server) handleDeepLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URI string `json:"uri"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if !strings.Contains(body.URI, ":") {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "uri must be absolute", nil)
		return
	}
	if _, err := s.Probe.Run.Run(r.Context(), "am", "start", "-a", "android.intent.action.VIEW",
		"-d", body.URI); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "deep link failed", err)
		return
	}
	s.Audit.Info("apps: deep link %s", body.URI)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleInstall installs an APK already uploaded to tmp/.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if !strings.HasPrefix(body.Path, s.Paths.Tmp+"/") {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"path must be a file inside the daemon tmp dir", nil)
		return
	}
	if _, err := os.Stat(body.Path); err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "uploaded file not found", err)
		return
	}
	if _, err := s.Probe.Run.Run(r.Context(), "pm", "install", "-r", body.Path); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "pm install failed", err)
		return
	}
	s.Audit.Info("apps: installed %s", body.Path)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUninstall is destructive: it needs the confirmation handshake.
func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	s.destructive(w, r, "apps/uninstall", func(ctx context.Context, pkg string) error {
		_, err := s.Probe.Run.Run(ctx, "pm", "uninstall", pkg)
		return err
	})
}

// handleClearData is destructive: it needs the confirmation handshake.
func (s *Server) handleClearData(w http.ResponseWriter, r *http.Request) {
	s.destructive(w, r, "apps/clearData", func(ctx context.Context, pkg string) error {
		_, err := s.Probe.Run.Run(ctx, "pm", "clear", pkg)
		return err
	})
}

// handleReboot is destructive: it needs the confirmation handshake.
func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConfirmationID string `json:"confirmation_id"`
		Reason         string `json:"reason"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.ConfirmationID == "" {
		id := newConfirmation("device/reboot", nil)
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
			"error": map[string]any{
				"code":    httpx.CodeConfirmation,
				"message": "reboot requires confirmation",
				"detail":  "repeat the request with confirmation_id",
			},
			"confirmation_id": id,
			"expires_in_sec":  int(confirmTTL.Seconds()),
		})
		return
	}
	if err := takeConfirmation(body.ConfirmationID, "device/reboot", nil); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeConfirmation, "invalid confirmation", err)
		return
	}
	s.Audit.Warn("device: reboot requested (%s)", body.Reason)
	if err := s.Device.Reboot(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "reboot failed", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) destructive(w http.ResponseWriter, r *http.Request, action string, run func(context.Context, string) error) {
	var body struct {
		Package        string `json:"package"`
		ConfirmationID string `json:"confirmation_id"`
		Reason         string `json:"reason"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Package == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "package is required", nil)
		return
	}
	params := map[string]string{"package": body.Package}
	if body.ConfirmationID == "" {
		id := newConfirmation(action, params)
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
			"error": map[string]any{
				"code":    httpx.CodeConfirmation,
				"message": action + " requires confirmation",
				"detail":  "repeat the request with confirmation_id",
			},
			"confirmation_id": id,
			"expires_in_sec":  int(confirmTTL.Seconds()),
		})
		return
	}
	if err := takeConfirmation(body.ConfirmationID, action, params); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeConfirmation, "invalid confirmation", err)
		return
	}
	if err := run(r.Context(), body.Package); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, action+" failed", err)
		return
	}
	s.Audit.Warn("%s: %s (%s)", action, body.Package, body.Reason)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSessionCompat answers the older claim shape some clients still send.
//
// It is not a second API: it maps `{tag, display_id}` onto the session contract so
// an existing client keeps working while the response carries the canonical
// fields. Anything else is rejected exactly as before.
func (s *Server) handleClaimCompat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent     string `json:"agent"`
		Tag       string `json:"tag"`
		DisplayID *int   `json:"display_id"`
		Display   *int   `json:"display"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Agent == "" {
		body.Agent = body.Tag
	}
	if body.Agent == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"agent (or tag) is required", nil)
		return
	}
	if body.Display == nil {
		body.Display = body.DisplayID
	}
	if body.Display == nil {
		zero := 0
		body.Display = &zero
	}
	s.claimSession(w, r, body.Agent, *body.Display)
}

// handleAppPermissions grants or revokes a permission/appop.
func (s *Server) handleAppPermissions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Package    string `json:"package"`
		Permission string `json:"permission"`
		Grant      *bool  `json:"grant"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Package == "" || body.Permission == "" || body.Grant == nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"package, permission and grant are required", nil)
		return
	}
	if sess := s.sessionFromRequest(r); sess == nil && s.Sessions.Count() > 0 {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeExclusiveNeeded,
			"a permission change needs a session and no other active session", nil)
		return
	}
	verb := "revoke"
	if *body.Grant {
		verb = "grant"
	}
	if _, err := s.Probe.Run.Run(r.Context(), "pm", verb, body.Package, body.Permission); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "pm "+verb+" failed", err)
		return
	}
	s.Audit.Info("apps: %s %s %s", verb, body.Package, body.Permission)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleFiles lists or stats a path.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "path is required", nil)
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "stat failed", err)
		return
	}
	if !st.IsDir() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"type": "file", "path": path, "size": st.Size(), "mode": st.Mode().String(),
			"modified": st.ModTime().Format(time.RFC3339),
		})
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "read dir failed", err)
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"name": e.Name(), "dir": e.IsDir(), "size": info.Size(),
			"modified": info.ModTime().Format(time.RFC3339),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"type": "dir", "path": path, "count": len(out), "entries": out})
}

// handleFileDownload streams a file.
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "path is required", nil)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "open failed", err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "not a regular file", nil)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// handleFileDelete removes a path (file or empty directory).
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Path == "" || body.Path == "/" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "refusing to delete this path", nil)
		return
	}
	if err := os.Remove(body.Path); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "delete failed", err)
		return
	}
	s.Audit.Warn("files: deleted %s", body.Path)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleFileMove renames a path.
func (s *Server) handleFileMove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.From == "" || body.To == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "from and to are required", nil)
		return
	}
	if err := os.MkdirAll(filepath.Dir(body.To), 0o755); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "mkdir failed", err)
		return
	}
	if err := os.Rename(body.From, body.To); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "move failed", err)
		return
	}
	s.Audit.Info("files: moved %s -> %s", body.From, body.To)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUpload streams the request body into the daemon tmp dir and returns the
// path, so a follow-up install/import can use it.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "upload.bin"
	}
	name = filepath.Base(name)
	if strings.Contains(name, "..") {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid name", nil)
		return
	}
	if err := os.MkdirAll(s.Paths.Tmp, 0o700); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "tmp dir", err)
		return
	}
	path := filepath.Join(s.Paths.Tmp, name)
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	f, err := os.Create(path)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "create failed", err)
		return
	}
	n, err := f.ReadFrom(r.Body)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(path)
		var maxErr *http.MaxBytesError
		if ok := asMaxBytes(err, &maxErr); ok {
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "upload too large", err)
			return
		}
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "upload failed", err)
		return
	}
	_ = os.Chmod(path, 0o600)
	s.Audit.Info("files: uploaded %s (%d bytes)", name, n)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "bytes": n})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
