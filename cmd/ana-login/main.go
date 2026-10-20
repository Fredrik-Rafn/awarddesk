// ana-login performs explicit, bounded live login checks; never flight searches.
package main

import (
	"awarddesk/internal/award"
	"awarddesk/internal/config"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

type result struct {
	Run            int    `json:"run"`
	Success        bool   `json:"success"`
	DurationMS     int64  `json:"durationMs"`
	Retries        int    `json:"preCredentialRetries"`
	Code           string `json:"code,omitempty"`
	Stage          string `json:"stage,omitempty"`
	Message        string `json:"message,omitempty"`
	OutcomeUnknown bool   `json:"outcomeUnknown,omitempty"`
}

func main() { os.Exit(run()) }
func run() int {
	runs := flag.Int("runs", 1, "sequential live checks (1-10); each uses a fresh proxy lease and may consume solver credits")
	reportPath := flag.String("report", "", "optional sanitized JSON report")
	flag.Parse()
	if *runs < 1 || *runs > 10 {
		fmt.Fprintln(os.Stderr, "runs must be 1-10")
		return 1
	}
	if err := config.LoadEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	p, ready, err := config.ANA()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ready {
		fmt.Fprintln(os.Stderr, "Configure ANA_MEMBER_NUMBER, ANA_PASSWORD and HYPER_API_KEY")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	results := []result{}
	failed := false
	for i := 1; i <= *runs; i++ {
		started := time.Now()
		row := result{Run: i}
		s, err := p.Login(ctx, func(progress award.Progress) {
			if progress.Stage == "retry" {
				row.Retries++
			}
			fmt.Fprintf(os.Stderr, "Run %d +%.2fs: %s\n", i, time.Since(started).Seconds(), progress.Message)
		})
		row.DurationMS = time.Since(started).Milliseconds()
		terminal := false
		if err == nil {
			s.Close()
			row.Success = true
		} else {
			failed = true
			row.Code, row.Message = "login_failed", "ANA login failed"
			var ae *award.Error
			if errors.As(err, &ae) {
				row.Code, row.Stage, row.Message, row.OutcomeUnknown = ae.Code, ae.Stage, ae.Message, ae.OutcomeUnknown
				terminal = ae.Code == "rate_limited" || ae.Code == "authentication" || ae.Code == "mfa_required" || ae.Code == "login_response" || ae.Code == "proxy_authentication"
			}
			if ctx.Err() != nil {
				row.Code, row.Message = "cancelled", "Login checks cancelled"
				terminal = true
			}
		}
		results = append(results, row)
		fmt.Printf("Run %d: success=%t duration=%.2fs retries=%d code=%s stage=%s\n", i, row.Success, float64(row.DurationMS)/1000, row.Retries, row.Code, row.Stage)
		if !row.Success {
			fmt.Fprintln(os.Stderr, row.Message)
		}
		if terminal {
			fmt.Fprintln(os.Stderr, "Stopped: account, proxy credentials, cancellation or rate-limit response requires attention")
			break
		}
	}
	if *reportPath != "" {
		data, err := json.MarshalIndent(struct {
			At        time.Time `json:"at"`
			ProxyPool bool      `json:"proxyPool"`
			Results   []result  `json:"results"`
		}{time.Now().UTC(), os.Getenv("AWARDDESK_PROXY_FILE") != "", results}, "", "  ")
		if err != nil || os.WriteFile(*reportPath, data, 0600) != nil {
			fmt.Fprintln(os.Stderr, "Could not save report")
			return 1
		}
	}
	if failed {
		return 1
	}
	return 0
}
