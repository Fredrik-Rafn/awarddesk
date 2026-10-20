package main

import (
	"awarddesk/internal/config"
	"awarddesk/internal/outlook"
	"context"
	"fmt"
	"os"
	"time"
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
	reader := outlook.New(outlook.Config{Email: os.Getenv("OUTLOOK_EMAIL"), ClientID: os.Getenv("OUTLOOK_CLIENT_ID"), RefreshToken: os.Getenv("OUTLOOK_REFRESH_TOKEN")})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	_, err := reader.Checkpoint(ctx)
	if err == nil {
		fmt.Println("Outlook OAuth2 and read-only IMAP inbox access verified. No email bodies printed or messages modified.")
	}
	return err
}
