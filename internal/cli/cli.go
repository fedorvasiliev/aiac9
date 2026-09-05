// Package cli implements the command-line entry points of the application.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/interactive"
	"github.com/fedorvasiliev/aiac9/internal/server"
	"github.com/fedorvasiliev/aiac9/internal/version"
)

// Execute parses os.Args and dispatches to the requested subcommand. Called
// without arguments, it starts the interactive console wizard.
func Execute() {
	if len(os.Args) < 2 {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, "config error:", err)
			os.Exit(1)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if err := interactive.Run(ctx, cfg, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "interactive error:", err)
			os.Exit(1)
		}
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
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`aiac9 — usage:

  aiac9                 interactive question/answer console mode
  aiac9 serve [flags]   start the HTTP server
  aiac9 version         print version info
  aiac9 help            show this help`)
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
