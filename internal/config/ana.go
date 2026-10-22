package config

import (
	"os"

	"awarddesk/internal/antibot"
	"awarddesk/internal/providers/ana"
	"awarddesk/internal/proxies"
	"awarddesk/internal/storage"
)

// ANA constructs an explicit environment-only diagnostic provider. The server
// and default CLI use ANAFromDatabase instead.
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

// ANAFromDatabase constructs the login-only provider using persisted settings.
func ANAFromDatabase(db *storage.Store) (*ana.Provider, bool, error) {
	m, err := db.Settings()
	if err != nil {
		return nil, false, err
	}
	c := ana.Config{MemberNumber: m["ANA_MEMBER_NUMBER"], Password: m["ANA_PASSWORD"], Proxy: m["AWARDDESK_PROXY"]}
	if key := m["HYPER_API_KEY"]; key != "" {
		c.Challenge = antibot.Hyper{Key: key, VerifyAtLogin: true, SkipPixel: true}
	}
	if m["PROXY_LIST"] != "" {
		c.Pool = proxies.NewSource(func() (string, error) {
			settings, err := db.Settings()
			return settings["PROXY_LIST"], err
		})
	}
	return ana.New(c), c.MemberNumber != "" && c.Password != "" && c.Challenge != nil, nil
}
