package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"syscall"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/liamsysmind/roost/internal/auth"
	"github.com/liamsysmind/roost/internal/config"
	"github.com/liamsysmind/roost/internal/server"
	"github.com/liamsysmind/roost/internal/session"
)

const version = "0.0.0-dev"

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
  roost setup [--config PATH]      create config with password + session secret
  roost serve [--config PATH] [--addr HOST:PORT]
                                   run the HTTP server
  roost version
`)
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

	am, err := auth.NewManager(cfg.Auth.PasswordHash, cfg.Auth.SessionSecret)
	if err != nil {
		log.Fatal(err)
	}

	sessCfg, err := cfg.ResolveSession()
	if err != nil {
		log.Fatal(err)
	}
	sm := session.NewManager(session.Config{
		LogDir:      sessCfg.LogDir,
		ReplayBytes: sessCfg.ReplayBytes,
		IdleTTL:     sessCfg.IdleTTL,
	})
	defer sm.Shutdown()

	log.Fatal(server.New(am, sm, cfg.Server.Addr).Run())
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

	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		log.Fatal(err)
	}

	cfg := &config.Config{
		Auth: config.Auth{
			PasswordHash:  string(hash),
			SessionSecret: hex.EncodeToString(secret[:]),
		},
		Server: config.Server{Addr: "127.0.0.1:8080"},
		Session: config.Session{
			// LogDir empty → resolves to $XDG_DATA_HOME/roost/sessions
			// or ~/.local/share/roost/sessions.
			ReplayKB: 4096, // 4 MB replay on attach; full log still on disk.
			IdleTTL:  "24h",
		},
	}
	if err := config.Save(*cfgPath, cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %s. Run `roost serve` to start.\n", *cfgPath)
}
