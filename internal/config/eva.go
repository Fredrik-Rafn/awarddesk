package config

import (
	"awarddesk/internal/outlook"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/proxies"
	"fmt"
	"os"
)

// EVA is shared by the server and diagnostic command.
func EVA() (*eva.Provider, bool, error) {
	c := eva.Config{
		Email: os.Getenv("EVA_EMAIL"), Password: os.Getenv("EVA_PASSWORD"),
		HyperKey: os.Getenv("HYPER_API_KEY"), AnyKey: os.Getenv("ANYSOLVER_API_KEY"),
		ImpervaSolver: os.Getenv("EVA_IMPERVA_SOLVER"),
	}
	if c.ImpervaSolver == "" {
		c.ImpervaSolver = "hyper"
	}
	if c.ImpervaSolver != "hyper" && c.ImpervaSolver != "anysolver" {
		return nil, false, fmt.Errorf("EVA_IMPERVA_SOLVER must be hyper or anysolver")
	}
	m := outlook.Config{Email: os.Getenv("OUTLOOK_EMAIL"), ClientID: os.Getenv("OUTLOOK_CLIENT_ID"), RefreshToken: os.Getenv("OUTLOOK_REFRESH_TOKEN")}
	c.Mailbox = outlook.New(m)
	if path := os.Getenv("AWARDDESK_PROXY_FILE"); path != "" {
		var err error
		c.Pool, err = proxies.New(path)
		if err != nil {
			return nil, false, err
		}
	}
	ready := c.Email != "" && c.Password != "" && c.HyperKey != "" && c.AnyKey != "" && c.Pool != nil && m.Email != "" && m.ClientID != "" && m.RefreshToken != ""
	return eva.New(c), ready, nil
}
