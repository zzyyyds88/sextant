// Package ctl implements the local maintenance subcommands.
//
// These run as root from an adb shell or from the APK's single privileged call
// (`secret set-unlock`). They are the only way to mint a pairing code, which is
// what makes adb the trusted first-authentication channel (DESIGN D24).
package ctl

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"chopin-core/internal/audit"
	"chopin-core/internal/config"
	"chopin-core/internal/engine"
	"chopin-core/internal/pairing"
	"chopin-core/internal/status"
	"chopin-core/internal/sysprobe"
	"chopin-core/internal/token"
)

// Run dispatches a ctl subcommand. args excludes the program name and "ctl".
func Run(args []string, version string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	cfg, _, err := config.Load(engine.NewPaths(config.DefaultDataDir).Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	paths := engine.NewPaths(cfg.DataDir)

	switch args[0] {
	case "version":
		fmt.Printf("chopin-core %s (schema %d)\n", version, config.SchemaVersion)
		return 0
	case "status":
		return cmdStatus(cfg, paths)
	case "pair":
		return cmdPair(paths, args[1:])
	case "token":
		return cmdToken(paths, args[1:])
	case "secret":
		return cmdSecret(cfg, paths, args[1:])
	case "animation":
		return cmdAnimation(paths, args[1:])
	case "audit", "log":
		return cmdLog(paths, args[1:])
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown ctl command %q\n\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Print(`chopin-core ctl — local maintenance (runs as root)

  version                      print the binary version and schema
  status [--json]              print the published status (reads state/status.json)
  pair new --agent NAME        mint a one-time pairing code (prints it once)
  token list                   list issued tokens (metadata only)
  token revoke --agent NAME    revoke every token for an agent
  token rotate --agent NAME    replace an agent's token (prints the new one once)
  secret set-unlock            read the unlock pattern from stdin, store it 0600
  secret clear-unlock          delete the unlock pattern
  secret set-sim               read the SIM PIN from stdin, store it 0600
  secret clear-sim             delete the SIM PIN
  secret status                report credential presence only
  animation off|restore        disable window animations, or restore the baseline
  log [n]                      show the last n audit log lines

The pairing code is the only way to obtain an API token: it must come from a
channel you already trust (adb + su), never from the network.
`)
}

func cmdStatus(cfg config.Config, paths engine.Paths) int {
	data, err := os.ReadFile(paths.Status)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: the daemon has not published status yet (is it running?)\n")
		return 1
	}
	var st status.Status
	if err := json.Unmarshal(data, &st); err != nil {
		fmt.Fprintf(os.Stderr, "status: parse: %v\n", err)
		return 1
	}
	if len(os.Args) > 2 && os.Args[2] == "--json" {
		os.Stdout.Write(data)
		return 0
	}
	fmt.Printf("daemon  : pid %d  version %s  up %ds  %s\n", st.Daemon.PID, st.Daemon.Version, st.Daemon.UptimeSec, st.Daemon.State)
	fmt.Printf("package : %s  installed=%v\n", st.App.Package, st.App.Installed)
	fmt.Printf("a11y    : %-9s %s (%s)\n", st.App.A11y.State, st.App.A11y.Detail, st.App.A11y.Component)
	fmt.Printf("bg      : autostart=%s bgControl=%s doze=%v ok=%v\n",
		st.App.Bg.Autostart, st.App.Bg.BgControl, st.App.Bg.Doze, st.App.Bg.OK)
	for _, g := range sortedKeys(st.App.Perms) {
		fmt.Printf("perm    : %-14s %s\n", g, st.App.Perms[g])
	}
	fmt.Printf("unlock  : credential=%s locked=%v last=%s\n", st.Unlock.Credential, st.Unlock.ScreenLock, st.Unlock.LastAttempt)
	fmt.Printf("sessions: %d/%d\n", st.Sessions.Active, st.Sessions.Max)
	for _, e := range st.Errors {
		fmt.Printf("error   : %s %s %s\n", e.At, e.Code, e.Message)
	}
	return 0
}

func cmdPair(paths engine.Paths, args []string) int {
	if len(args) == 0 || args[0] != "new" {
		fmt.Fprintln(os.Stderr, "usage: ctl pair new --agent NAME")
		return 2
	}
	fs := flag.NewFlagSet("pair new", flag.ContinueOnError)
	agent := fs.String("agent", "pc", "agent name")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	m, err := pairing.Open(paths.Pairing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pairing: %v\n", err)
		return 1
	}
	code, st, err := m.New(*agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pairing: %v\n", err)
		return 1
	}
	a := audit.Open(paths.Log, 1<<20, 500)
	a.Info("pair: minted code for %s (expires %d)", *agent, st.ExpiresAt)
	// The code goes to stdout exactly once and is never written to the log.
	fmt.Println(code)
	return 0
}

func cmdToken(paths engine.Paths, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ctl token list|revoke|rotate")
		return 2
	}
	store, err := token.Open(paths.Tokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tokens: %v\n", err)
		return 1
	}
	switch args[0] {
	case "list":
		recs := store.List()
		if len(recs) == 0 {
			fmt.Println("(no tokens)")
			return 0
		}
		fmt.Println("agent\tcreated\tscopes\tlocal_only")
		for _, r := range recs {
			fmt.Printf("%s\t%d\t%s\t%v\n", r.Agent, r.CreatedAt, strings.Join(r.Scopes, ","), r.LocalOnly)
		}
		return 0
	case "revoke", "rotate":
		fs := flag.NewFlagSet("token "+args[0], flag.ContinueOnError)
		agent := fs.String("agent", "", "agent name")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *agent == "" {
			fmt.Fprintln(os.Stderr, "--agent is required")
			return 2
		}
		if args[0] == "revoke" {
			n, err := store.Revoke(*agent)
			if err != nil {
				fmt.Fprintf(os.Stderr, "revoke: %v\n", err)
				return 1
			}
			fmt.Printf("revoked %d token(s) for %s\n", n, *agent)
			return 0
		}
		tok, rec, err := store.Rotate(*agent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rotate: %v\n", err)
			return 1
		}
		fmt.Printf("rotated %s; scopes=%s\n%s\n", rec.Agent, strings.Join(rec.Scopes, ","), tok)
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown token subcommand %q\n", args[0])
	return 2
}

// cmdSecret stores the unlock pattern. The value arrives on stdin so it never
// appears in a process list, and is written 0600 owned by root.
func cmdSecret(cfg config.Config, paths engine.Paths, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ctl secret set-unlock|clear-unlock|set-sim|clear-sim|status")
		return 2
	}
	unlock := cfg.Path(cfg.Unlock.CredentialFile)
	sim := ""
	if cfg.Unlock.SIMPinFile != "" {
		sim = cfg.Path(cfg.Unlock.SIMPinFile)
	}
	switch args[0] {
	case "status":
		reportSecret("unlock credential", unlock)
		if sim == "" {
			fmt.Println("SIM PIN: disabled (unlock.sim_pin_file is empty)")
		} else {
			reportSecret("SIM PIN", sim)
		}
		return 0
	case "clear-unlock":
		if err := removeSecret(unlock); err != nil {
			fmt.Fprintf(os.Stderr, "clear-unlock: %v\n", err)
			return 1
		}
		audit.Open(paths.Log, 1<<20, 500).Info("secret: unlock credential cleared")
		fmt.Println("cleared")
		return 0
	case "set-unlock":
		value, err := readSecretStdin()
		if err != nil {
			fmt.Fprintf(os.Stderr, "set-unlock: %v\n", err)
			return 1
		}
		if err := writeSecret(unlock, value); err != nil {
			fmt.Fprintf(os.Stderr, "set-unlock: %v\n", err)
			return 1
		}
		// Length only: never the value.
		audit.Open(paths.Log, 1<<20, 500).Info("secret: unlock credential stored (%d digits)", len(value))
		fmt.Println("stored")
		return 0
	case "set-sim":
		if sim == "" {
			fmt.Fprintln(os.Stderr, "set-sim: unlock.sim_pin_file is empty; enable the SIM PIN path in core.json first")
			return 1
		}
		value, err := readSecretStdin()
		if err != nil {
			fmt.Fprintf(os.Stderr, "set-sim: %v\n", err)
			return 1
		}
		value = strings.TrimSpace(value)
		if !validSIMPin(value) {
			fmt.Fprintln(os.Stderr, "set-sim: SIM PIN must be 4-8 digits")
			return 1
		}
		if err := writeSecret(sim, value); err != nil {
			fmt.Fprintf(os.Stderr, "set-sim: %v\n", err)
			return 1
		}
		audit.Open(paths.Log, 1<<20, 500).Info("secret: SIM PIN stored (%d digits)", len(value))
		fmt.Println("stored")
		return 0
	case "clear-sim":
		if sim == "" {
			fmt.Fprintln(os.Stderr, "clear-sim: unlock.sim_pin_file is empty")
			return 1
		}
		if err := removeSecret(sim); err != nil {
			fmt.Fprintf(os.Stderr, "clear-sim: %v\n", err)
			return 1
		}
		audit.Open(paths.Log, 1<<20, 500).Info("secret: SIM PIN cleared")
		fmt.Println("cleared")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown secret subcommand %q\n", args[0])
	return 2
}

// reportSecret prints presence and mode only; a credential value is never read
// back to the caller.
func reportSecret(name, path string) {
	st, err := os.Stat(path)
	if err != nil {
		fmt.Printf("%s: absent\n", name)
		return
	}
	fmt.Printf("%s: present (%d bytes, mode %s)\n", name, st.Size(), st.Mode().Perm())
}

func removeSecret(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validSIMPin(v string) bool {
	if len(v) < 4 || len(v) > 8 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func readSecretStdin() (string, error) {
	st, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if st.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("refusing to read the credential from a terminal; pipe it on stdin")
	}
	var b strings.Builder
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func writeSecret(path, value string) error {
	if value == "" {
		return fmt.Errorf("empty credential")
	}
	for _, r := range value {
		if r < '1' || r > '9' {
			return fmt.Errorf("credential must be digits 1-9")
		}
	}
	dir := path
	if i := strings.LastIndex(path, "/"); i > 0 {
		dir = path[:i]
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	return os.Rename(tmp, path)
}

func cmdLog(paths engine.Paths, args []string) int {
	n := 40
	if len(args) > 0 {
		if _, err := fmt.Sscanf(args[0], "%d", &n); err != nil {
			fmt.Fprintf(os.Stderr, "log: %v\n", err)
			return 2
		}
	}
	lines, err := audit.Open(paths.Log, 1<<20, 500).Tail(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log: %v\n", err)
		return 1
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return 0
}

func sortedKeys(m status.Perms) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

var _ = sysprobe.ExecRunner{}
var _ = context.Background
