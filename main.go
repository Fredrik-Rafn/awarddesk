package main

import (
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
	"syscall"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/config"
	"awarddesk/internal/fleet"
	"awarddesk/internal/jobs"
	"awarddesk/internal/login"
	"awarddesk/internal/providers/ana"
	"awarddesk/internal/registration"
	"awarddesk/internal/session"
	"awarddesk/internal/sessionstore"
	"awarddesk/internal/web"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	port := flag.Int("port", 8080, "localhost HTTP port")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		return errors.New("invalid port")
	}
	dbPath := config.DatabasePath()
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
	proxy, err := session.ParseProxy(settings["AWARDDESK_PROXY"])
	if err != nil {
		return err
	}
	baProvider, ready, err := config.BAFromSettings(settings)
	if err != nil {
		return err
	}
	evaProvider := fleet.New(db)
	defer evaProvider.Close()
	evaReady := evaProvider.Ready()
	var providers []award.Provider
	if ready {
		providers = append(providers, baProvider)
	}
	providers = append(providers, evaProvider)
	j := jobs.NewConcurrent(award.ConcurrencyLimit(settings["SEARCH_CONCURRENCY"]), providers...)
	if err = j.SetPersistence(db); err != nil {
		return err
	}
	defer j.Close()
	app, err := web.New(j, ready || evaReady, proxy != "" || settings["PROXY_LIST"] != "")
	if err != nil {
		return err
	}
	registrations, err := registration.New(db, evaProvider)
	if err != nil {
		return err
	}
	defer registrations.Close()
	app.Admin = &web.Admin{DB: db, Fleet: evaProvider, Registrations: registrations, Searches: j}
	app.Availability = func() (bool, bool) {
		return ready || evaProvider.Ready(), db.Setting("PROXY_LIST") != "" || proxy != ""
	}
	var check login.Check
	anaProvider, anaReady, err := config.ANAFromDatabase(db)
	if err != nil {
		return err
	}
	if anaReady {
		manager := ana.NewManager(anaProvider, sessionstore.Store{Backend: db, Path: filepath.Join(".state", "ana", anaProvider.AccountID()+".json")})
		check = manager.Ensure
	}
	app.ANALogin = login.New(check)
	defer app.ANALogin.Close()
	app.EVALogin = login.NewNamed("EVA", 6*time.Minute, evaProvider.Ensure)
	defer app.EVALogin.Close()
	srv := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
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
		if settings["ANA_BACKGROUND_ENABLED"] != "false" {
			app.ANALogin.Maintain(ctx)
		}
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
	err = srv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
