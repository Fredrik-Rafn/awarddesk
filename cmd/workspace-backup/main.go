package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"awarddesk/internal/config"
	"awarddesk/internal/storage"
)

func main() {
	source := flag.String("source", config.DatabasePath(), "source SQLite database")
	out := flag.String("out", "", "new backup filename")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "Specify -out with a new backup filename")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := storage.Backup(ctx, *source, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Verified private workspace backup: %s\n", *out)
}
