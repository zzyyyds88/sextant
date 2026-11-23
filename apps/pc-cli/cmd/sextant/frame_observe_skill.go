package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"sextant/internal/client"
)

// cmdFrame fetches one JPEG frame from the daemon.
//
// Unlike `screenshot`, this is the binary path: one request, image bytes, no
// second download. Default output is frame.jpg in the current directory.
func cmdFrame(a *args) error {
	cfg, err := client.Load()
	if err != nil {
		return fmt.Errorf("not paired: %w", err)
	}
	c, err := client.New(cfg)
	if err != nil {
		return err
	}
	display := a.optStr("display", "0")
	quality := a.optStr("quality", "80")
	maxSize := a.optStr("max-size", "0")
	query := "/v1/ui/frame?display_id=" + display + "&quality=" + quality + "&max_size=" + maxSize
	if a.has("cached") {
		query += "&cached=1"
	}
	data, err := c.Get(query)
	if err != nil {
		return err
	}
	out := a.optStr("o", "frame.jpg")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s (%d bytes)\n", out, len(data))
	return nil
}

// cmdObserve asks for several observations of one frame in one request.
func cmdObserve(a *args) error {
	body := map[string]any{}
	if a.has("tree") {
		body["tree"] = true
	}
	if a.has("ocr") {
		body["ocr"] = true
	}
	if a.has("pixels") {
		body["pixels"] = true
	}
	if a.has("frame") {
		body["frame"] = true
	}
	if len(body) == 0 {
		return fmt.Errorf("choose at least one of --tree, --ocr, --pixels, --frame")
	}
	if q := a.optStr("quality", ""); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			body["quality"] = n
		}
	}
	if m := a.optStr("max-size", ""); m != "" {
		if n, err := strconv.Atoi(m); err == nil {
			body["max_size"] = n
		}
	}
	return cmdPost("/v1/ui/observe", body)
}

// cmdSkill downloads the agent manual, or installs it as a skill directory.
//
// It works before pairing: /doc/skill is public. The server address comes from
// the pairing config when present, otherwise from the default serial's host.
func cmdSkill(a *args) error {
	server := ""
	if cfg, err := client.Load(); err == nil {
		server = cfg.Server
	} else {
		host := defaultSerial
		if host == "" {
			return fmt.Errorf("no paired server: run sextant pair first or set SEXTANT_SERIAL")
		}
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		server = host + ":8443"
	}
	path := "/doc/skill"
	if a.has("zip") {
		path = "/doc/skill.zip"
	}
	data, err := client.PublicGET(server, path)
	if err != nil {
		return err
	}
	if install := a.optStr("install", ""); install != "" {
		if a.has("zip") {
			return fmt.Errorf("--install expects the Markdown manual; drop --zip")
		}
		if err := os.MkdirAll(install, 0o755); err != nil {
			return err
		}
		out := filepath.Join(install, "SKILL.md")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Printf("installed %s (%d bytes)\n", out, len(data))
		return nil
	}
	if out := a.optStr("o", ""); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Printf("%s (%d bytes)\n", out, len(data))
		return nil
	}
	if a.has("zip") {
		return fmt.Errorf("binary zip output needs -o FILE")
	}
	fmt.Print(string(data))
	return nil
}
