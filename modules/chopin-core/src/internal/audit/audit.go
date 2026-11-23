// Package audit writes the append-only operation log.
//
// Rotation is by size (DESIGN §10.4): when the file exceeds maxBytes the last
// keepLines lines are kept. Nothing secret is ever written here: no tokens, no
// unlock credentials, no input text (length only).
package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Log is a size-rotated line log.
type Log struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keepLine int
}

// Open wires the log to a path with a size cap.
func Open(path string, maxBytes int, keepLines int) *Log {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	if keepLines <= 0 {
		keepLines = 500
	}
	return &Log{path: path, maxBytes: int64(maxBytes), keepLine: keepLines}
}

// Write appends one structured line.
func (l *Log) Write(level, format string, args ...any) {
	line := fmt.Sprintf("%s [%s] %s\n",
		time.Now().Format("2006-01-02 15:04:05"), level, fmt.Sprintf(format, args...))
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	l.rotateLocked()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

// Info logs an operation outcome.
func (l *Log) Info(format string, args ...any) { l.Write("INFO", format, args...) }

// Warn logs a recoverable problem.
func (l *Log) Warn(format string, args ...any) { l.Write("WARN", format, args...) }

// Error logs a failure that the user may need to act on.
func (l *Log) Error(format string, args ...any) { l.Write("ERROR", format, args...) }

func (l *Log) rotateLocked() {
	st, err := os.Stat(l.path)
	if err != nil || st.Size() <= l.maxBytes {
		return
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > l.keepLine {
		lines = lines[len(lines)-l.keepLine:]
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}

// Tail returns the last n lines (used by `ctl log`).
func (l *Log) Tail(n int) ([]string, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// Path is where the log lives (for diagnostics).
func (l *Log) Path() string { return l.path }
