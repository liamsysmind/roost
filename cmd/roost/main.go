package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/liamsysmind/roost/internal/auth"
	"github.com/liamsysmind/roost/internal/config"
	rfs "github.com/liamsysmind/roost/internal/fs"
	"github.com/liamsysmind/roost/internal/notify"
	"github.com/liamsysmind/roost/internal/server"
	"github.com/liamsysmind/roost/internal/session"
)

var version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "setup":
		runSetup(os.Args[2:])
	case "hook-info":
		runHookInfo(os.Args[2:])
	case "notify-stop":
		runNotifyStop(os.Args[2:])
	case "version":
		fmt.Println("roost", version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`roost — self-hosted workspace for AI agents

Usage:
  roost setup [--config PATH]      create config with password + secrets
  roost serve [--config PATH] [--addr HOST:PORT]
                                   run the HTTP server
  roost hook-info [--config PATH]  print example Claude Code Stop hook
  roost notify-stop [--config PATH]
                                   reads Claude Code Stop hook JSON on stdin,
                                   posts a cwd-tagged notification
  roost version
`)
}

func runHookInfo(args []string) {
	fs := flag.NewFlagSet("hook-info", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "path to config.toml")
	_ = fs.Parse(args)

	if _, err := config.Load(*cfgPath); err != nil {
		log.Fatal(err)
	}

	// Resolve the absolute path to this binary so the snippet works under
	// launchd-spawned hooks where PATH is minimal.
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	cmd := exe + " notify-stop"
	if *cfgPath != config.DefaultPath() {
		cmd += " --config " + shellQuote(*cfgPath)
	}

	snippet := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": cmd,
						},
					},
				},
			},
		},
	}
	fmt.Println("# Add to ~/.claude/settings.json to push a cwd-tagged notification when Claude Code stops:")
	fmt.Println()
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snippet); err != nil {
		log.Fatal(err)
	}
}

// shellQuote single-quotes s for safe inclusion in a /bin/sh command line.
// Embedded single quotes become '\''.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runNotifyStop is the binary-side companion to the hook snippet printed by
// runHookInfo. It reads Claude Code's Stop hook JSON on stdin, extracts cwd,
// and POSTs a notification with the hook secret. Designed so the hook config
// can be a single command with no shell-quoting traps and no jq/python
// dependency.
func runNotifyStop(args []string) {
	fs := flag.NewFlagSet("notify-stop", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "path to config.toml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.Auth.HookSecret == "" {
		log.Fatal("config has no hook_secret; run `roost setup` to regenerate")
	}

	// Stop hook stdin shape (best-effort — unknown fields ignored).
	var input struct {
		Cwd       string `json:"cwd"`
		SessionID string `json:"session_id"`
	}
	raw, _ := io.ReadAll(os.Stdin)
	_ = json.Unmarshal(raw, &input) // empty / malformed → cwd just stays ""

	form := url.Values{}
	form.Set("title", "Claude Code idle")
	form.Set("body", "Agent stopped, waiting for input")
	if input.Cwd != "" {
		form.Set("cwd", input.Cwd)
	}
	if input.SessionID != "" {
		form.Set("session", input.SessionID)
	}

	addr := cfg.Server.Addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	req, err := http.NewRequest(http.MethodPost,
		"http://"+addr+"/api/notify",
		strings.NewReader(form.Encode()))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Roost-Hook-Secret", cfg.Auth.HookSecret)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		log.Fatalf("POST /api/notify: %s — %s", resp.Status, strings.TrimSpace(string(b)))
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "path to config.toml")
	addr := fs.String("addr", "", "override listen address (e.g. 127.0.0.1:8080)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}

	am := auth.NewManager(cfg.Auth.PasswordHash)

	sessCfg, err := cfg.ResolveSession()
	if err != nil {
		log.Fatal(err)
	}
	sm, err := session.NewManager(session.Config{
		LogDir:      sessCfg.LogDir,
		ReplayBytes: sessCfg.ReplayBytes,
		IdleTTL:     sessCfg.IdleTTL,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sm.Shutdown()

	fsAPI, err := rfs.New(cfg.FS.Root)
	if err != nil {
		log.Fatal(err)
	}

	n := notify.NewNotifier()

	// Validated in config.Load; ParseDuration is safe here.
	idleAlert, _ := time.ParseDuration(cfg.Notify.IdleAlertAfter)

	log.Fatal(server.New(am, sm, fsAPI, n, cfg.Auth.HookSecret, cfg.Server.Addr, version, idleAlert).Run())
}

func runSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "path to config.toml")
	passwordStdin := fs.Bool("password-stdin", false, "read password from stdin (one line) instead of prompting")
	force := fs.Bool("force", false, "overwrite existing config")
	_ = fs.Parse(args)

	if _, err := os.Stat(*cfgPath); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "%s already exists. Use --force to overwrite.\n", *cfgPath)
		os.Exit(1)
	}

	var pw []byte
	if *passwordStdin {
		var err error
		pw, err = io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		pw = bytes.TrimRight(pw, "\r\n")
	} else {
		fmt.Print("Set a password for roost: ")
		var err error
		pw, err = term.ReadPassword(syscall.Stdin)
		fmt.Println()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print("Confirm:                  ")
		pw2, err := term.ReadPassword(syscall.Stdin)
		fmt.Println()
		if err != nil {
			log.Fatal(err)
		}
		if string(pw) != string(pw2) {
			log.Fatal("passwords don't match")
		}
	}
	if len(pw) < 8 {
		log.Fatal("password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword(pw, 12)
	if err != nil {
		log.Fatal(err)
	}

	var hookSecret [32]byte
	if _, err := rand.Read(hookSecret[:]); err != nil {
		log.Fatal(err)
	}

	addr, fallback := pickListenAddr()
	cfg := &config.Config{
		Auth: config.Auth{
			PasswordHash: string(hash),
			HookSecret:   hex.EncodeToString(hookSecret[:]),
		},
		Server: config.Server{Addr: addr},
		Session: config.Session{
			// LogDir empty → resolves to $XDG_DATA_HOME/roost/sessions
			// or ~/.local/share/roost/sessions.
			ReplayKB: 4096, // 4 MB replay on attach; full log still on disk.
			IdleTTL:  "24h",
		},
	}
	_ = fallback
	if err := config.Save(*cfgPath, cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %s.\n", *cfgPath)
	if fallback {
		fmt.Printf("Listen address: %s (8080 was busy; another user or service holds it.)\n", addr)
		fmt.Printf("SSH tunnel from your laptop:\n  ssh -L 8080:localhost:%s user@host\n", portOf(addr))
	} else {
		fmt.Printf("Listen address: %s\n", addr)
	}
	fmt.Println("Run `roost serve` to start.")
}

// pickListenAddr scans 127.0.0.1:8080..8199 and returns the first port we
// can bind. The boolean is true when we had to fall back to a non-default
// port (8080 was already taken).
func pickListenAddr() (string, bool) {
	for port := 8080; port < 8200; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		_ = l.Close()
		return addr, port != 8080
	}
	return "127.0.0.1:8080", false
}

func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return "8080"
}
