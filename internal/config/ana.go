package config

import (
	"awarddesk/internal/antibot"
	"awarddesk/internal/providers/ana"
	"awarddesk/internal/proxies"
	"os"
)

// ANA constructs the same provider configuration for the server and CLI.
func ANA() (*ana.Provider, bool, error) {
	c := ana.Config{MemberNumber: os.Getenv("ANA_MEMBER_NUMBER"), Password: os.Getenv("ANA_PASSWORD"), Proxy: os.Getenv("AWARDDESK_PROXY")}
	key := os.Getenv("HYPER_API_KEY")
	if path := os.Getenv("AWARDDESK_PROXY_FILE"); path != "" {
		pool, err := proxies.New(path)
		if err != nil {
			return nil, false, err
		}
		c.Pool = pool
	}
	if key != "" {
		c.Challenge = antibot.Hyper{Key: key, VerifyAtLogin: true, SkipPixel: true}
	}
	return ana.New(c), c.MemberNumber != "" && c.Password != "" && key != "", nil
}
