// Package cli implements the command-line entry points of the application.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/interactive"
	"github.com/fedorvasiliev/aiac9/internal/server"
	"github.com/fedorvasiliev/aiac9/internal/version"
)

// Execute parses os.Args and dispatches to the requested subcommand. Called
// without arguments — or with only flags, e.g. `aiac9 -f w1d4` — it starts
// the interactive console wizard.
func Execute() {
	if len(os.Args) < 2 {
		runInteractive(nil)
		return
	}

	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "version":
		fmt.Println(version.String())
	case "help", "-h", "--help":
		printUsage()
	default:
		if strings.HasPrefix(os.Args[1], "-") {
			runInteractive(os.Args[1:])
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`aiac9 — usage:

  aiac9 [-f substr] [-timeout seconds]   interactive console wizard mode
  aiac9 serve [flags]                    start the HTTP server
  aiac9 version                          print version info
  aiac9 help                             show this help

interactive mode flags:
  -f substr        only list ./prompts files whose name contains substr
  -timeout seconds override the response timeout (default: 180s)`)
}

func runInteractive(args []string) {
	fs := flag.NewFlagSet("aiac9", flag.ExitOnError)
	filter := fs.String("f", "", "only list ./prompts files whose name contains this substring")
	timeoutSec := fs.Int("timeout", 0, "override the response timeout, in seconds")
	fs.Parse(args) // flag.ExitOnError: exits the process on a parse error

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := interactive.Options{PromptFilter: *filter}
	if *timeoutSec > 0 {
		d := time.Duration(*timeoutSec) * time.Second
		opts.ResponseTimeout = &d
	}

	if err := interactive.Run(ctx, cfg, os.Stdin, os.Stdout, opts); err != nil {
		fmt.Fprintln(os.Stderr, "interactive error:", err)
		os.Exit(1)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "", "address to listen on (overrides ADDR env var)")
	fs.Parse(args) // flag.ExitOnError: exits the process on a parse error

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	if *addr != "" {
		cfg.Addr = *addr
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "server error:", err)
		os.Exit(1)
	}
}
