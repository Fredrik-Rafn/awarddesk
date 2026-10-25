package config

import (
	"errors"
	"net"
	"os"
	"strconv"
)

type HTTPOptions struct {
	Host      string
	Port      int
	PublicURL string
}

func HTTP() (HTTPOptions, error) {
	c := HTTPOptions{Host: os.Getenv("AWARDDESK_LISTEN_HOST"), Port: 8080, PublicURL: os.Getenv("AWARDDESK_PUBLIC_URL")}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if raw := os.Getenv("PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return c, errors.New("PORT must be an integer from 1 to 65535")
		}
		c.Port = port
	}
	if c.PublicURL == "" && os.Getenv("RAILWAY_PUBLIC_DOMAIN") != "" {
		c.PublicURL = "https://" + os.Getenv("RAILWAY_PUBLIC_DOMAIN")
	}
	return c, nil
}

func (c HTTPOptions) Address() (string, error) {
	if c.Port < 1 || c.Port > 65535 {
		return "", errors.New("invalid port")
	}
	ip := net.ParseIP(c.Host)
	if c.Host != "localhost" && ip == nil {
		return "", errors.New("listen host must be localhost or an IP address")
	}
	if c.Host != "localhost" && !ip.IsLoopback() && c.PublicURL == "" {
		return "", errors.New("public listening requires AWARDDESK_PUBLIC_URL or RAILWAY_PUBLIC_DOMAIN and a demo password")
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), nil
}
