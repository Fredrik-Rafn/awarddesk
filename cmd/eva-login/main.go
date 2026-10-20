package main

import (
	"awarddesk/internal/award"
	"awarddesk/internal/config"
	"context"
	"fmt"
	"os"
	"os/signal"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if err := config.LoadEnv(".env"); err != nil {
		return err
	}
	p, ready, err := config.EVA()
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("EVA, Outlook, proxy and solver configuration is incomplete")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return p.Ensure(ctx, func(p award.Progress) { fmt.Printf("%s: %s\n", p.Stage, p.Message) })
}
