package main

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/config"
	"awarddesk/internal/fleet"
	"awarddesk/internal/jobs"
	"awarddesk/internal/login"
	"awarddesk/internal/providers/ana"
	"awarddesk/internal/providers/ba"
	"awarddesk/internal/registration"
	"awarddesk/internal/session"
	"awarddesk/internal/sessionstore"
	"awarddesk/internal/web"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run() error {
	dbPath := os.Getenv("AWARDDESK_DB")
	if dbPath == "" {
		dbPath = filepath.Join("data", "awarddesk.db")
	}
	db, err := config.Database()
	if err != nil {
		return err
	}
	defer db.Close()
	releaseWorkspace, err := (sessionstore.Store{Path: dbPath + ".server"}).Lock()
	if err != nil {
		return errors.New("this workspace already has a running server")
	}
	defer releaseWorkspace()
	settings, err := db.Settings()
	if err != nil {
		return err
	}
	port := flag.Int("port", 8080, "localhost HTTP port")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		return errors.New("invalid port")
	}
	proxy, e := session.ParseProxy(settings["AWARDDESK_PROXY"])
	if e != nil {
		return e
	}
	email, password := settings["BA_EMAIL"], settings["BA_PASSWORD"]
	anyKey, hyperKey := settings["ANYSOLVER_API_KEY"], settings["HYPER_API_KEY"]
	ready := email != "" && password != "" && anyKey != "" && hyperKey != ""
	p := ba.New(ba.Config{Email: email, Password: password, Proxy: proxy, Captcha: antibot.AnySolver{Key: anyKey, Proxy: proxy}, Challenge: antibot.Hyper{Key: hyperKey}})
	evaProvider := fleet.New(db)
	defer evaProvider.Close()
	evaReady := evaProvider.Ready()
	var providers []award.Provider
	if ready {
		providers = append(providers, &serializedProvider{Provider: p, gate: make(chan struct{}, 1)})
	}
	providers = append(providers, evaProvider)
	j := jobs.NewConcurrent(16, providers...)
	if err = j.SetPersistence(db); err != nil {
		return err
	}
	defer j.Close()
	app, e := web.New(j, ready || evaReady, proxy != "" || settings["PROXY_LIST"] != "")
	if e != nil {
		return e
	}
	registrations, err := registration.New(db, evaProvider)
	if err != nil {
		return err
	}
	defer registrations.Close()
	app.Admin = &web.Admin{DB: db, Fleet: evaProvider, Registrations: registrations}
	app.Availability = func() (bool, bool) {
		return ready || evaProvider.Ready(), db.Setting("PROXY_LIST") != "" || proxy != ""
	}
	var check login.Check
	anaProvider, anaReady, e := config.ANAFromDatabase(db)
	if e != nil {
		return e
	}
	if anaReady {
		manager := ana.NewManager(anaProvider, sessionstore.Store{Backend: db, Path: filepath.Join(".state", "ana", anaProvider.AccountID()+".json")})
		check = manager.Ensure
	}
	app.ANALogin = login.New(check)
	defer app.ANALogin.Close()
	var evaCheck login.Check
	evaCheck = evaProvider.Ensure
	app.EVALogin = login.NewNamed("EVA", 6*time.Minute, evaCheck)
	defer app.EVALogin.Close()
	srv := &http.Server{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)), Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	evaProvider.Start()
	registrations.Start()
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		var maintenance sync.WaitGroup
		if settings["ANA_BACKGROUND_ENABLED"] != "false" {
			maintenance.Add(1)
			go func() { defer maintenance.Done(); app.ANALogin.Maintain(ctx) }()
		}

		maintenance.Wait()
	}()
	defer func() { stop(); <-maintenanceDone }()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Printf("Awarddesk: http://%s\n", srv.Addr)
	if !ready && !evaReady {
		log.Print("Add accounts, proxies and solver keys at /admin to enable searches")
	}
	e = srv.Serve(listener)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

// Legacy single-account providers retain exclusive account ownership.
type serializedProvider struct {
	award.Provider
	gate chan struct{}
}

func (p *serializedProvider) Search(ctx context.Context, q award.Query, r award.Reporter) (award.Result, error) {
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
		return p.Provider.Search(ctx, q, r)
	case <-ctx.Done():
		return award.Result{Query: q}, ctx.Err()
	}
}
