package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Session is the local handle for the write session this computer claimed.
// It is single-user, single-device state: concurrent agents must use their own
// HTTP clients and send X-Chopin-Session themselves (D37).
type Session struct {
	Server string `json:"server"`
	ID     string `json:"id"`
	Token  string `json:"token"`
	Agent  string `json:"agent"`
}

// SessionPath is where the local claim lives.
func SessionPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "session.json"), nil
}

// LoadSession reads the local claim. A missing file is not an error: most
// commands run without a session.
func LoadSession() (Session, error) {
	var s Session
	path, err := SessionPath()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// SaveSession writes the local claim 0600.
func SaveSession(s Session) error {
	path, err := SessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ClearSession removes the local claim. A missing file is fine.
func ClearSession() error {
	path, err := SessionPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
