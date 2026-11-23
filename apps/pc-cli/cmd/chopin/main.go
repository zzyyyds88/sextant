// Command chopin drives a chopin device from a computer.
//
// The first command you run is `chopin pair`, which mints a one-time code on the
// device over ADB (a channel you already trust) and trades it for an API token
// (DESIGN D24). After that every command is a plain HTTPS call with a pinned
// certificate.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"chopin/internal/client"
)

// defaultSerial is the adb serial used when --serial is omitted. It is not
// baked into the build: set CHOPIN_SERIAL for a default, or pass --serial.
var defaultSerial = os.Getenv("CHOPIN_SERIAL")

// cliVersion is the CLI's own version, kept in step with the module.
var cliVersion = "3.0.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(help())
		os.Exit(2)
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 && args[1] == "--json" {
			if err := printHelpJSON(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		fmt.Print(help())
		return
	}
	cmd, ok := lookupCommand(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		fmt.Print(help())
		os.Exit(2)
	}
	parsed, err := parseArgs(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if err := runCommand(cmd, parsed); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serialArg(args []string) (string, []string) {
	serial := defaultSerial
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--serial" && i+1 < len(args) {
			serial = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return serial, rest
}

func agentArg(args []string) (string, []string) {
	agent := "pc"
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--agent" && i+1 < len(args) {
			agent = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return agent, rest
}

// cmdPair mints a code on the device via adb and exchanges it for a token.
//
// Requiring adb here is the point: the code never travels over the network, so a
// machine that can reach the phone's port still cannot pair without physical /
// adb access.
func cmdPair(args []string) error {
	serial, rest := serialArg(args)
	if serial == "" {
		return fmt.Errorf("missing device serial: pass --serial <host:port> or set CHOPIN_SERIAL")
	}
	agent, _ := agentArg(rest)

	// The adb serial is host:port-of-adbd (<host>:<adb-port>), while the daemon
	// listens on 8443. They share a host and differ in port, so the host is taken
	// from the serial and the port is the daemon's.
	host := serial
	if i := strings.LastIndex(serial, ":"); i > 0 {
		host = serial[:i]
	}
	device := host + ":8443"

	fmt.Println("==> check the daemon is reachable")
	health, pin, err := client.Unpaired(device)
	if err != nil {
		return fmt.Errorf("cannot reach https://%s/v1/health: %w\n"+
			"    is the module installed and the phone unlocked once since boot?", device, err)
	}
	if ok, _ := health["ok"].(bool); !ok {
		return fmt.Errorf("daemon answered but is not healthy: %v", health)
	}
	fmt.Printf("    version %v, schema %v\n", health["version"], health["schema"])

	fmt.Println("==> mint a one-time pairing code on the device (adb)")
	out, err := adb(serial, "su -c 'chopin-core ctl pair new --agent "+agent+"'")
	if err != nil {
		return fmt.Errorf("adb pairing code failed: %w\n    output: %s", err, out)
	}
	code := lastNonEmptyLine(out)
	if len(code) < 8 {
		return fmt.Errorf("device did not return a pairing code (got %q)", code)
	}

	fmt.Println("==> exchange the code for an API token")
	resp, err := client.PostUnpaired(device, code, agent)
	if err != nil {
		return err
	}
	token, _ := resp["token"].(string)
	if token == "" {
		return fmt.Errorf("pair response has no token: %v", resp)
	}
	// Prefer the fingerprint the server reported during the exchange; fall back
	// to the one observed on /v1/health (they must be the same certificate).
	if fp, _ := resp["server_cert_sha256"].(string); fp != "" {
		pin = fp
	}
	if err := client.Save(client.Config{
		Server: device, Token: token, Agent: agent, CertSHA256: pin,
		PairedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		return err
	}
	path, _ := client.ConfigPath()
	fmt.Printf("paired with %s as %q; config: %s (0600)\n", device, agent, path)
	return nil
}

func adb(serial, shellCmd string) (string, error) {
	cmd := exec.Command("adb", "-s", serial, "shell", shellCmd)
	// adb needs the authorized key, which this project keeps in a dedicated HOME
	// (adbhome/). Honour CHOPIN_ADB_HOME when set, otherwise use the normal HOME.
	if home := os.Getenv("CHOPIN_ADB_HOME"); home != "" {
		cmd.Env = append(os.Environ(), "HOME="+home)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

func paired() (*client.Client, error) {
	cfg, err := client.Load()
	if err != nil {
		return nil, err
	}
	return client.New(cfg)
}

func cmdHealth() error {
	// Health is public, so it works before pairing — but it is also the check
	// that catches an unusable pairing, so try the paired URL first.
	cfg, err := client.Load()
	if err != nil {
		return err
	}
	out, pin, err := client.Unpaired(cfg.Server)
	if err != nil {
		return err
	}
	if pin != cfg.CertSHA256 {
		return fmt.Errorf("certificate pin changed: paired with %s, server now presents %s\n"+
			"    if you reinstalled the module, pair again", cfg.CertSHA256, pin)
	}
	pretty, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(pretty))
	return nil
}

func cmdStatus() error {
	c, err := paired()
	if err != nil {
		return err
	}
	out, err := c.Get("/v1/status")
	if err != nil {
		return err
	}
	return printJSON(out)
}

func cmdGet(path string) error {
	c, err := paired()
	if err != nil {
		return err
	}
	out, err := c.Get(path)
	if err != nil {
		return err
	}
	return printJSON(out)
}

func cmdPost(path string, body any) error {
	c, err := paired()
	if err != nil {
		return err
	}
	out, err := c.Post(path, body)
	if err != nil {
		return err
	}
	return printJSON(out)
}

func cmdTap(args []string) error {
	_, rest := serialArg(args)
	if len(rest) < 2 {
		return fmt.Errorf("usage: chopin tap X Y")
	}
	var x, y int
	if _, err := fmt.Sscanf(rest[0]+" "+rest[1], "%d %d", &x, &y); err != nil {
		return fmt.Errorf("x and y must be integers")
	}
	return cmdPost("/v1/input/tap", map[string]any{"x": x, "y": y})
}

func cmdSwipe(args []string) error {
	_, rest := serialArg(args)
	if len(rest) < 4 {
		return fmt.Errorf("usage: chopin swipe X1 Y1 X2 Y2 [duration_ms]")
	}
	var v [5]int
	v[4] = 300
	if _, err := fmt.Sscanf(strings.Join(rest[:min(5, len(rest))], " "), "%d %d %d %d %d",
		&v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
		// Five values were not given; parse the four required ones.
		if _, err2 := fmt.Sscanf(strings.Join(rest[:4], " "), "%d %d %d %d", &v[0], &v[1], &v[2], &v[3]); err2 != nil {
			return fmt.Errorf("usage: chopin swipe X1 Y1 X2 Y2 [duration_ms]")
		}
	}
	return cmdPost("/v1/input/swipe", map[string]any{
		"x1": v[0], "y1": v[1], "x2": v[2], "y2": v[3], "duration_ms": v[4],
	})
}

func cmdKey(args []string) error {
	_, rest := serialArg(args)
	if len(rest) < 1 {
		return fmt.Errorf("usage: chopin key KEYCODE_HOME")
	}
	return cmdPost("/v1/input/key", map[string]any{"key": rest[0]})
}

// cmdShot captures the current frame in one request.
//
// D28/D29: /v1/ui/frame already returns the JPEG bytes. The old path asked the
// daemon to write a file, then pulled it through adb+su — a second hop that also
// named JPEG data `.png` and broke consumers that trusted the extension.
func cmdShot(args []string) error {
	_, rest := serialArg(args)
	out := "screenshot.jpg"
	for i := 0; i < len(rest); i++ {
		if (rest[i] == "-o" || rest[i] == "--out") && i+1 < len(rest) {
			out = rest[i+1]
			i++
		}
	}
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := c.Get("/v1/ui/frame?quality=80")
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("daemon returned an empty frame")
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Printf("%s (%d bytes)\n", out, len(data))
	return nil
}

func cmdToken(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: chopin token show|forget")
	}
	switch args[0] {
	case "show":
		cfg, err := client.Load()
		if err != nil {
			return err
		}
		fmt.Printf("server : %s\nagent  : %s\npin    : %s\npaired : %s\ntoken  : (stored, not printed)\n",
			cfg.Server, cfg.Agent, cfg.CertSHA256, cfg.PairedAt)
		return nil
	case "forget":
		path, err := client.ConfigPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("local pairing deleted")
		return nil
	}
	return fmt.Errorf("unknown token subcommand %q", args[0])
}

// printHelpJSON is the machine-readable command table. Agents should not parse
// the human help text to discover the surface.
func printHelpJSON() error {
	items := make([]map[string]string, 0, len(commands))
	for _, c := range commands {
		items = append(items, map[string]string{
			"name": c.name, "group": c.group, "usage": c.usage, "summary": c.summary,
		})
	}
	pretty, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(pretty))
	return nil
}

func printJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		// Not JSON: print verbatim rather than swallowing it.
		fmt.Println(string(data))
		return nil
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(string(data))
		return nil
	}
	fmt.Println(string(pretty))
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
