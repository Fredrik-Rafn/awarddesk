package config

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/providers/ba"
	"awarddesk/internal/session"
)

// BAFromSettings builds the legacy single-account provider from a settings
// snapshot. Unlike the EVA fleet, BA configuration changes require a restart.
func BAFromSettings(settings map[string]string) (*ba.Provider, bool, error) {
	proxy, err := session.ParseProxy(settings["AWARDDESK_PROXY"])
	if err != nil {
		return nil, false, err
	}
	c := ba.Config{
		Email: settings["BA_EMAIL"], Password: settings["BA_PASSWORD"], Proxy: proxy,
	}
	if key := settings["ANYSOLVER_API_KEY"]; key != "" {
		c.Captcha = antibot.AnySolver{Key: key, Proxy: proxy}
	}
	if key := settings["HYPER_API_KEY"]; key != "" {
		c.Challenge = antibot.Hyper{Key: key}
	}
	ready := c.Email != "" && c.Password != "" && c.Captcha != nil && c.Challenge != nil
	return ba.New(c), ready, nil
}
