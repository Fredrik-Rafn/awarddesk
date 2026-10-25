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
	"syscall"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/config"
	"awarddesk/internal/fleet"
	"awarddesk/internal/jobs"
	"awarddesk/internal/login"
	"awarddesk/internal/registration"
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
	httpConfig, err := config.HTTP()
	if err != nil {
		return err
	}
	port := flag.Int("port", httpConfig.Port, "HTTP port")
	host := flag.String("host", httpConfig.Host, "HTTP listen address")
	flag.Parse()
	httpConfig.Host, httpConfig.Port = *host, *port
	address, err := httpConfig.Address()
	if err != nil {
		return err
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
	evaProvider := fleet.New(db)

	defer evaProvider.Close()
	evaReady := evaProvider.Ready()
	j := jobs.NewConcurrent(award.ConcurrencyLimit(settings["SEARCH_CONCURRENCY"]), evaProvider)
	if err = j.SetPersistence(db); err != nil {
		return err
	}
	defer j.Close()
	app, err := web.New(j, evaReady, settings["PROXY_LIST"] != "")
	if err != nil {
		return err
	}
	if err = app.ConfigureDemo(httpConfig.PublicURL, os.Getenv("AWARDDESK_DEMO_PASSWORD")); err != nil {
		return err
	}
	registrations, err := registration.New(db, evaProvider)
	if err != nil {
		return err
	}
	defer registrations.Close()
	app.Admin = &web.Admin{DB: db, Fleet: evaProvider, Registrations: registrations, Searches: j}
	app.Availability = func() (bool, bool) {
		return evaProvider.Ready(), db.HasProxies()
	}
	app.EVALogin = login.New(evaProvider.Ensure)
	defer app.EVALogin.Close()
	srv := &http.Server{
		Addr:              address,
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
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Printf("Awarddesk: http://%s\n", srv.Addr)
	if !evaReady {
		log.Print("Add accounts, proxies and solver keys at /admin to enable searches")
	}
	err = srv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
