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
	db, err := config.Database()
	if err != nil {
		return err
	}
	defer db.Close()
	accounts, err := db.Accounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return fmt.Errorf("Add an EVA account in Admin first")
	}
	a := accounts[0]
	reader := outlook.New(outlook.Config{Email: a.OutlookEmail, ClientID: a.ClientID, RefreshToken: a.RefreshToken, StateBackend: db})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	_, err = reader.Checkpoint(ctx)
	if err == nil {
		fmt.Println("Outlook OAuth2 and read-only IMAP inbox access verified. No email bodies printed or messages modified.")
	}
	return err
}
