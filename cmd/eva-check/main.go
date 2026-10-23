package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
)

type measurement struct {
	Number         int              `json:"number"`
	Query          award.Query      `json:"query"`
	JobID          string           `json:"jobId,omitempty"`
	Status         string           `json:"status"`
	Complete       bool             `json:"complete"`
	Cached         bool             `json:"cached"`
	DurationMS     int64            `json:"durationMs"`
	FirstResultsMS int64            `json:"firstResultsMs,omitempty"`
	FirstQuoteMS   int64            `json:"firstQuoteMs,omitempty"`
	Itineraries    int              `json:"itineraries"`
	Quotes         int              `json:"quotes"`
	Recoveries     []award.Recovery `json:"recoveries,omitempty"`
	Error          *award.Error     `json:"error,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
}

type client struct {
	base, csrf string
	http       *http.Client
}

func (c client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("local API connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure award.Error
		if json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&failure) == nil && failure.Code != "" {
			return &failure
		}
		return fmt.Errorf("local API returned HTTP %d", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(output)
}

func save(path string, results []measurement) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".eva-check-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	base := flag.String("url", "http://127.0.0.1:8080", "local Awarddesk URL")
	casesPath := flag.String("cases", "", "JSON array of EVA search queries")
	out := flag.String("out", "", "JSON measurements file (rewritten after each run)")
	limit := flag.Int("limit", 0, "maximum cases; zero runs all")
	flag.Parse()
	u, err := url.Parse(*base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return errors.New("url must be a local HTTP address")
	}
	if *casesPath == "" || *out == "" {
		return errors.New("provide -cases and -out paths")
	}
	data, err := os.ReadFile(*casesPath)
	if err != nil {
		return errors.New("cannot read case file")
	}
	if len(data) > 1<<20 {
		return errors.New("case file too large")
	}
	var cases []award.Query
	if json.Unmarshal(data, &cases) != nil || len(cases) == 0 || len(cases) > 100 {
		return errors.New("case file must contain 1–100 queries")
	}
	if *limit < 0 {
		return errors.New("limit must not be negative")
	}
	if *limit > 0 && *limit < len(cases) {
		cases = cases[:*limit]
	}
	for i := range cases {
		cases[i].Refresh = true
		if cases[i].Provider != "eva" || cases[i].Validate(time.Now()) != nil {
			return fmt.Errorf("invalid EVA query at case %d", i+1)
		}
	}
	if err = os.MkdirAll(filepath.Dir(*out), 0700); err != nil {
		return errors.New("cannot create report directory")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c := client{base: u.Scheme + "://" + u.Host, http: &http.Client{Timeout: 20 * time.Second}}
	var cfg struct{ CSRF string }
	if err = c.request(ctx, "GET", "/api/config", nil, &cfg); err != nil {
		return err
	}
	c.csrf = cfg.CSRF
	var results []measurement
	for i, q := range cases {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		started := time.Now()
		m := measurement{Number: i + 1, Query: q}
		var job jobs.Job
		fmt.Printf("Starting %d/%d: %s-%s %s %s\n", i+1, len(cases), q.Origin, q.Destination, q.Date, q.Cabin)
		err = c.request(ctx, "POST", "/api/searches", q, &job)
		if err == nil {
			m.JobID = job.ID
			for job.Status == "queued" || job.Status == "running" {
				select {
				case <-ctx.Done():
					cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = c.request(cancelCtx, "DELETE", "/api/searches/"+job.ID, nil, nil)
					cancel()
					return ctx.Err()
				case <-time.After(time.Second):
				}
				if err = c.request(ctx, "GET", "/api/searches/"+job.ID, nil, &job); err != nil {
					break
				}
				observePreview(&m, job, time.Since(started).Milliseconds())
			}
		}
		m.DurationMS = time.Since(started).Milliseconds()
		observePreview(&m, job, m.DurationMS)
		m.Status = job.Status
		m.Error = job.Error
		m.Recoveries = job.Recoveries
		if err != nil {
			m.Status = "api_failed"
			var failure *award.Error
			if errors.As(err, &failure) {
				m.Error = failure
			} else {
				m.Error = &award.Error{Code: "local_api", Message: err.Error()}
			}
		}
		if job.Result != nil {
			m.Complete = job.Result.Complete
			m.Cached = job.Result.Cached
			m.Itineraries = len(job.Result.Itineraries)
			m.Warnings = job.Result.Warnings
			for _, item := range job.Result.Itineraries {
				for _, fare := range item.Fares {
					if fare.MilesCash != nil || (fare.PricingType == "award" && fare.Points > 0) {
						m.Quotes++
					}
				}
			}
		}
		results = append(results, m)
		if err = save(*out, results); err != nil {
			return errors.New("cannot save measurements")
		}
		encoded, _ := json.Marshal(m)
		fmt.Println(string(encoded))

		if stopAtFailure(m.Error) {
			fmt.Println("Stopped at an account, rate-limit or uncertain-outcome boundary.")
			break
		}
	}
	totals, _ := json.Marshal(summarize(results))
	fmt.Println("Summary: " + string(totals))
	return nil
}

func observePreview(m *measurement, job jobs.Job, elapsed int64) {
	if job.Result == nil || len(job.Result.Itineraries) == 0 {
		return
	}
	if m.FirstResultsMS == 0 {
		m.FirstResultsMS = elapsed
	}
	if m.FirstQuoteMS == 0 {
		for _, i := range job.Result.Itineraries {
			for _, f := range i.Fares {
				if f.MilesCash != nil || (f.PricingType == "award" && f.Points > 0) {
					m.FirstQuoteMS = elapsed
					return
				}
			}
		}
	}
}

func stopAtFailure(e *award.Error) bool {
	if e == nil {
		return false
	}
	accountStage := e.Stage == "credentials" || e.Stage == "otp submission"
	uncertainTransport := (e.Code == "connection" || e.Code == "response_read") && (accountStage || e.Stage == "miles pricing")
	return e.OutcomeUnknown || uncertainTransport || e.Code == "rate_limited" || e.Code == "authentication" || e.Code == "login_response" || e.Code == "local_api" || (e.Code == "session_denied" && accountStage)
}
