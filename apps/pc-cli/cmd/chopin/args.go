package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Flag parsing.
//
// The CLI is table-driven (see commands.go), so argument handling lives here once
// instead of being re-implemented per command. Every command accepts the same
// shapes: positionals in a fixed order, `--key value` options, `-o value` for the
// few short forms the help text prints, and `--json` for a raw body.

// args is a parsed argument list.
type args struct {
	pos  []string
	opts map[string]string
	raw  string
}

// boolFlags are the options that never take a value.
//
// Without this list `--ime` grabbed the *next* token as its value, so the natural
// spelling `chopin text --ime "你好"` parsed as text="true" and then failed with a
// usage error that said nothing about the real problem. Values are read from opts,
// so an explicit list of valueless flags is the smallest fix.
var boolFlags = map[string]bool{
	"ime": true, "clear": true, "click": true, "raw": true,
	"absent": true, "screenshot": true,
	"sim": true, "keep-animation": true, "restore": true,
	"tree": true, "ocr": true, "pixels": true, "frame": true, "zip": true, "cached": true,
	"expect-selected": true, "fresh": true, "summary": true, "clear-task": true,
	"allow-danger": true, "precheck-selected": true, "viewport": true,
}

// shortFlags maps the single-dash spellings the help text prints onto the long
// name the handler reads. `chopin get PATH -o FILE` is documented and used to be
// rejected outright ("usage: chopin get PATH -o FILE") because only `--` tokens
// were parsed as options at all.
var shortFlags = map[string]string{"o": "o"}

func parseArgs(argv []string) (*args, error) {
	a := &args{opts: map[string]string{}}
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if tok == "--" {
			a.pos = append(a.pos, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			a.pos = append(a.pos, tok)
			continue
		}
		long := strings.HasPrefix(tok, "--")
		name := strings.TrimPrefix(strings.TrimPrefix(tok, "-"), "-")
		if !long {
			// Single dash. Only an alphabetic name is a flag: `-3` stays a positional
			// argument, because tap/swipe coordinates and negative region bounds are
			// ordinary values here.
			if !isFlagName(name) {
				a.pos = append(a.pos, tok)
				continue
			}
			if expanded, ok := shortFlags[name]; ok {
				name = expanded
			}
		}
		if eq := strings.Index(name, "="); eq >= 0 {
			a.opts[name[:eq]] = name[eq+1:]
			continue
		}
		if boolFlags[name] {
			a.opts[name] = "true"
			continue
		}
		if i+1 < len(argv) && !isOptionToken(argv[i+1]) {
			value := argv[i+1]
			i++
			// A rectangle is printed as `--region L T R B`, and nobody quotes it.
			// Taking one token left `region = "0"` and the command then failed with
			// "--region needs four numbers" on the exact spelling its own usage line
			// shows, so the remaining numeric tokens are consumed here.
			if name == "region" {
				for len(strings.Fields(value)) < 4 && i+1 < len(argv) && !isOptionToken(argv[i+1]) {
					value += " " + argv[i+1]
					i++
				}
			}
			a.opts[name] = value
			continue
		}
		a.opts[name] = "true"
	}
	a.raw = a.opts["json"]
	return a, nil
}

// isFlagName reports whether s looks like a flag name rather than a number.
func isFlagName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// isOptionToken reports whether the token would itself be parsed as an option, so
// it is never consumed as a value.
func isOptionToken(s string) bool {
	if s == "--" || strings.HasPrefix(s, "--") {
		return true
	}
	if !strings.HasPrefix(s, "-") {
		return false
	}
	return isFlagName(strings.TrimPrefix(s, "-"))
}

// str returns a positional or a default.
func (a *args) str(i int, def string) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return def
}

// num returns a positional parsed as an int.
func (a *args) num(i int, def int) (int, error) {
	if i >= len(a.pos) {
		return def, nil
	}
	n, err := strconv.Atoi(a.pos[i])
	if err != nil {
		return 0, fmt.Errorf("argument %d must be a number, got %q", i+1, a.pos[i])
	}
	return n, nil
}

// optInt returns an integer option (with a default).
func (a *args) optInt(name string, def int) (int, error) {
	v, ok := a.opts[name]
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("--%s must be a number, got %q", name, v)
	}
	return n, nil
}

// optFloat returns a floating-point option (with a default).
func (a *args) optFloat(name string, def float64) (float64, error) {
	v, ok := a.opts[name]
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("--%s must be a number, got %q", name, v)
	}
	return n, nil
}

// optStr returns a string option.
func (a *args) optStr(name, def string) string {
	if v, ok := a.opts[name]; ok {
		return v
	}
	return def
}

// has reports whether an option was given.
func (a *args) has(name string) bool {
	_, ok := a.opts[name]
	return ok
}

// need fails when fewer than n positionals were given.
func (a *args) need(n int, usage string) error {
	if len(a.pos) < n {
		return fmt.Errorf("usage: chopin %s", usage)
	}
	return nil
}
