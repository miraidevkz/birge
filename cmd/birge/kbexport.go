package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"birge/internal/wiring"
)

// runKBExport writes published knowledge versions to --out (ADR-0022).
func runKBExport(args []string) int {
	fs := flag.NewFlagSet("kb-export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "directory for the markdown export tree")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: birge kb-export --out <dir>")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("command", "kb-export")
	cfg, err := wiring.Load(os.Getenv, os.ReadFile, wiring.CommandKBExport)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 1
	}
	if err := wiring.RunKBExport(ctx, logger, cfg, *out); err != nil {
		logger.Error("exit", "error", err)
		return 1
	}
	return 0
}
