package main

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/award"
	"awarddesk/internal/config"
	"awarddesk/internal/jobs"
	"awarddesk/internal/login"
	"awarddesk/internal/providers/ana"
	"awarddesk/internal/providers/ba"
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
	if err := config.LoadEnv(".env"); err != nil {
		return err
	}
	port := flag.Int("port", 8080, "localhost HTTP port")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		return errors.New("invalid port")
	}
	proxy, e := session.ParseProxy(os.Getenv("AWARDDESK_PROXY"))
	if e != nil {
		return e
	}
	email, password := os.Getenv("BA_EMAIL"), os.Getenv("BA_PASSWORD")
	anyKey, hyperKey := os.Getenv("ANYSOLVER_API_KEY"), os.Getenv("HYPER_API_KEY")
	ready := email != "" && password != "" && anyKey != "" && hyperKey != ""
	p := ba.New(ba.Config{Email: email, Password: password, Proxy: proxy, Captcha: antibot.AnySolver{Key: anyKey, Proxy: proxy}, Challenge: antibot.Hyper{Key: hyperKey}})
	evaProvider, evaReady, e := config.EVA()
	if e != nil {
		return e
	}
	var providers []award.Provider
	if ready {
		providers = append(providers, p)
	}
	if evaReady {
		providers = append(providers, evaProvider)
	}
	j := jobs.New(providers...)
	defer j.Close()
	app, e := web.New(j, ready || evaReady, proxy != "" || os.Getenv("AWARDDESK_PROXY_FILE") != "")
	if e != nil {
		return e
	}
	var check login.Check
	anaProvider, anaReady, e := config.ANA()
	if e != nil {
		return e
	}
	if anaReady {
		manager := ana.NewManager(anaProvider, sessionstore.Store{Path: filepath.Join(".state", "ana", anaProvider.AccountID()+".session")})
		check = manager.Ensure
	}
	app.ANALogin = login.New(check)
	defer app.ANALogin.Close()
	var evaCheck login.Check
	if evaReady {
		evaCheck = evaProvider.Ensure
	}
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
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		var maintenance sync.WaitGroup
		if os.Getenv("ANA_BACKGROUND_ENABLED") != "false" {
			maintenance.Add(1)
			go func() { defer maintenance.Done(); app.ANALogin.Maintain(ctx) }()
		}
		if os.Getenv("EVA_BACKGROUND_ENABLED") != "false" && evaReady {
			maintenance.Add(1)
			go func() { defer maintenance.Done(); app.EVALogin.Maintain(ctx) }()
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
		log.Print("Search is disabled until account and solver environment variables are configured")
	}
	e = srv.Serve(listener)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
