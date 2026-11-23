// Package client is the HTTP client the `sextant` command uses.
//
// Two invariants matter here:
//
//   - The server certificate is pinned to the fingerprint recorded at pairing
//     time. A self-signed certificate means "any certificate" unless it is
//     pinned, and an unpinned client is trivially impersonated on a LAN.
//   - The token is stored per server (IP:port), because a second phone must not
//     silently reuse the first phone's credential.
package client

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is what pairing writes and every command reads.
type Config struct {
	Server     string `json:"server"`
	Token      string `json:"token"`
	CertSHA256 string `json:"cert_sha256"`
	Agent      string `json:"agent"`
	PairedAt   string `json:"paired_at"`
}

// Dir is the per-user config directory.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".sextant"), nil
}

// ConfigPath is where the paired configuration lives.
func ConfigPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// Load reads the paired configuration.
func Load() (Config, error) {
	var c Config
	path, err := ConfigPath()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, fmt.Errorf("not paired yet: run 'sextant pair --serial <adb>'")
		}
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Server == "" || c.Token == "" || c.CertSHA256 == "" {
		return c, fmt.Errorf("%s is incomplete; pair again", path)
	}
	return c, nil
}

// Save writes the configuration 0600.
func Save(c Config) error {
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", d, err)
	}
	path := filepath.Join(d, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Client is a paired connection.
type Client struct {
	cfg          Config
	http         *http.Client
	sessionToken string
}

// New builds a pinned client from a configuration.
func New(c Config) (*Client, error) {
	want := strings.ToLower(strings.TrimSpace(c.CertSHA256))
	if want == "" {
		return nil, fmt.Errorf("no certificate pin; pair again")
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Verification is done by the pinned fingerprint below, not by the
			// system trust store: the certificate is self-signed, so the store
			// would reject it and "accept everything" would accept an impostor.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return fmt.Errorf("server presented no certificate")
				}
				sum := sha256.Sum256(rawCerts[0])
				got := hex.EncodeToString(sum[:])
				if got != want {
					return fmt.Errorf("certificate pin mismatch: got %s, expected %s", got, want)
				}
				return nil
			},
		},
	}
	cli := &Client{cfg: c, http: &http.Client{Transport: tr, Timeout: 120 * time.Second}}
	// D37: a claim made by this computer is attached to every following request,
	// so business activity keeps the session fresh without a heartbeat per call.
	if sess, err := LoadSession(); err == nil && sess.Server == c.Server && sess.Token != "" {
		cli.sessionToken = sess.Token
	}
	return cli, nil
}

// Do performs a request against the paired server.
func (c *Client) Do(method, path string, body any) ([]byte, error) {
	return c.DoWithHeaders(method, path, body, nil)
}

// DoWithHeaders is Do plus caller-supplied request headers. The D30 human
// profile uses X-Sextant-Request-Id to make a multi-step request reproducible
// from one seed without making every step tap the same pixel.
func (c *Client) DoWithHeaders(method, path string, body any, headers map[string]string) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	url := "https://" + c.cfg.Server + path
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	if c.sessionToken != "" {
		req.Header.Set("X-Sextant-Session", c.sessionToken)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if c.sessionToken != "" && req.Header.Get("X-Sextant-Session") == "" {
		req.Header.Set("X-Sextant-Session", c.sessionToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// Get is Do("GET", …).
func (c *Client) Get(path string) ([]byte, error) { return c.Do("GET", path, nil) }

// Post is Do("POST", …).
func (c *Client) Post(path string, body any) ([]byte, error) { return c.Do("POST", path, body) }

// Put is Do("PUT", …).
func (c *Client) Put(path string, body any) ([]byte, error) { return c.Do("PUT", path, body) }

// Patch is Do("PATCH", …).
func (c *Client) Patch(path string, body any) ([]byte, error) { return c.Do("PATCH", path, body) }

// Delete is Do("DELETE", …).
func (c *Client) Delete(path string) ([]byte, error) { return c.Do("DELETE", path, nil) }

// PostRaw uploads a byte body verbatim (used by /v1/uploads, which takes the
// file itself rather than JSON).
func (c *Client) PostRaw(path string, data []byte) ([]byte, error) {
	url := "https://" + c.cfg.Server + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// StreamProbe opens an SSE endpoint and returns its headers without consuming the
// body: stream endpoints never end on their own, so a normal request would hang.
func (c *Client) StreamProbe(path string) (http.Header, int, error) {
	url := "https://" + c.cfg.Server + path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Transport: c.http.Transport, Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	return resp.Header, resp.StatusCode, nil
}

// Server is the paired endpoint.
func (c *Client) Server() string { return c.cfg.Server }

// Unpaired fetches health and the certificate fingerprint without a token.
//
// Pairing needs this: the computer has not been trusted yet, so it can only
// touch the two public endpoints and record the pin it will verify from then on.
func Unpaired(server string) (map[string]any, string, error) {
	var pin string
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("server presented no certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			pin = hex.EncodeToString(sum[:])
			return nil
		},
	}}
	cl := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	resp, err := cl.Get("https://" + server + "/v1/health")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, pin, fmt.Errorf("health is not JSON: %w", err)
	}
	return out, pin, nil
}

// PublicGET fetches a public read-only document (for example /doc/skill).
//
// No token is sent because there is nothing to authorize: these endpoints are
// public by contract and carry no credentials. TLS is not pinned here for the
// same reason it is not pinned during pairing — the caller has not established
// trust yet, and the content is not sensitive.
func PublicGET(server, path string) ([]byte, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
	}}
	cl := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	resp, err := cl.Get("https://" + server + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// PostUnpaired performs the pairing exchange itself.
func PostUnpaired(server, code, agent string) (map[string]any, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "agent": agent})
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}}
	cl := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	resp, err := cl.Post("https://"+server+"/v1/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("pair response is not JSON: %w (%s)", err, string(data))
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("pair rejected: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return out, nil
}
