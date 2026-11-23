package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"chopin/internal/client"
)

// cmdInit brings a freshly flashed device to a usable state in one command.
//
// What it deliberately keeps out of argv: the unlock pattern and the SIM PIN.
// Both are read from stdin and forwarded to `ctl secret` over adb's stdin, so
// they never appear in a process list, a shell history or this tool's output.
func cmdInit(args []string) error {
	serial, rest := serialArg(args)
	agent, _ := agentArg(rest)

	if hasFlag(rest, "--restore") {
		out, err := adb(serial, "su -c 'chopin-core ctl animation restore'")
		if err != nil {
			return fmt.Errorf("restore animations: %w\n    output: %s", err, strings.TrimSpace(out))
		}
		fmt.Println("animations restored")
		return nil
	}

	host := serial
	if i := strings.LastIndex(serial, ":"); i > 0 {
		host = serial[:i]
	}
	device := host + ":8443"

	fmt.Println("==> check the daemon")
	health, _, err := client.Unpaired(device)
	if err != nil {
		return fmt.Errorf("cannot reach https://%s/v1/health: %w\n"+
			"    is the module installed and the phone unlocked once since boot?", device, err)
	}
	fmt.Printf("    version %v, schema %v\n", health["version"], health["schema"])

	// A stored token that still answers /v1/status means this machine is already
	// paired; minting another one would leave a second live token behind.
	paired := false
	if cfg, err := client.Load(); err == nil {
		if c, err := client.New(cfg); err == nil {
			if _, err := c.Get("/v1/status"); err == nil {
				paired = true
			}
		}
	}
	if paired {
		fmt.Println("==> already paired")
	} else {
		if err := cmdPair([]string{"--serial", serial, "--agent", agent}); err != nil {
			return err
		}
	}

	// Credentials: line 1 is the unlock pattern. With --sim, line 2 is the PIN.
	// An empty line skips that credential.
	reader := bufio.NewReader(os.Stdin)
	pattern, err := readCredential(reader, "unlock pattern (empty to skip): ")
	if err != nil {
		return err
	}
	if pattern != "" {
		out, err := adbStdin(serial, "su -c 'chopin-core ctl secret set-unlock'", pattern+"\n")
		if err != nil {
			return fmt.Errorf("store unlock pattern: %w\n    output: %s", err, strings.TrimSpace(out))
		}
		fmt.Println("==> unlock pattern stored")
	} else {
		fmt.Println("==> unlock pattern skipped")
	}
	if hasFlag(rest, "--sim") {
		sim, err := readCredential(reader, "SIM PIN (empty to skip): ")
		if err != nil {
			return err
		}
		if sim != "" {
			out, err := adbStdin(serial, "su -c 'chopin-core ctl secret set-sim'", sim+"\n")
			if err != nil {
				return fmt.Errorf("store SIM PIN: %w\n    output: %s", err, strings.TrimSpace(out))
			}
			fmt.Println("==> SIM PIN stored")
		} else {
			fmt.Println("==> SIM PIN skipped")
		}
	}

	if !hasFlag(rest, "--keep-animation") {
		out, err := adb(serial, "su -c 'chopin-core ctl animation off'")
		if err != nil {
			return fmt.Errorf("disable animations: %w\n    output: %s", err, strings.TrimSpace(out))
		}
		fmt.Println("==> animations disabled (baseline recorded; `chopin init --restore` undoes it)")
	}

	if cfg, err := client.Load(); err == nil {
		if c, err := client.New(cfg); err == nil {
			if data, err := c.Get("/v1/status"); err == nil {
				var buf bytes.Buffer
				if json.Indent(&buf, data, "", "  ") == nil {
					fmt.Printf("\n==> device ready\n%s\n", buf.String())
					return nil
				}
			}
		}
	}
	fmt.Println("\n==> device ready (status not readable; run `chopin status`)")
	return nil
}

// readCredential reads one line. On a terminal it prints the prompt; when stdin
// is a pipe it stays quiet so scripted callers see only their own output.
func readCredential(r *bufio.Reader, prompt string) (string, error) {
	if stdinIsTTY() {
		fmt.Fprint(os.Stderr, prompt)
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && line == "" {
		return "", nil
	}
	return strings.TrimSpace(line), nil
}

func stdinIsTTY() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// adbStdin runs one shell command and feeds input over stdin.
//
// The credential path requires this: `ctl secret set-unlock` only reads stdin,
// which is what keeps the value out of argv.
func adbStdin(serial, shellCmd, input string) (string, error) {
	cmd := exec.Command("adb", "-s", serial, "shell", shellCmd)
	if home := os.Getenv("CHOPIN_ADB_HOME"); home != "" {
		cmd.Env = append(os.Environ(), "HOME="+home)
	}
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
