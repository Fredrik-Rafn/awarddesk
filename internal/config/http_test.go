package config

import "testing"

func TestHTTPDefaultsAndRailway(t *testing.T) {
	for _, key := range []string{"PORT", "AWARDDESK_LISTEN_HOST", "AWARDDESK_PUBLIC_URL", "RAILWAY_PUBLIC_DOMAIN"} {
		t.Setenv(key, "")
	}
	c, err := HTTP()
	if err != nil {
		t.Fatal(err)
	}
	if address, err := c.Address(); err != nil || address != "127.0.0.1:8080" {
		t.Fatal("local defaults changed")
	}
	t.Setenv("PORT", "9070")
	t.Setenv("AWARDDESK_LISTEN_HOST", "0.0.0.0")
	t.Setenv("RAILWAY_PUBLIC_DOMAIN", "awarddesk.up.railway.app")
	c, err = HTTP()
	if err != nil || c.PublicURL != "https://awarddesk.up.railway.app" {
		t.Fatal("Railway domain not selected")
	}
	if address, err := c.Address(); err != nil || address != "0.0.0.0:9070" {
		t.Fatal("Railway port not used")
	}
	t.Setenv("AWARDDESK_PUBLIC_URL", "https://custom.example.com")
	c, _ = HTTP()
	if c.PublicURL != "https://custom.example.com" {
		t.Fatal("explicit public URL was not preserved")
	}
}

func TestHTTPRejectsInvalidPortAndUnconfiguredPublicListener(t *testing.T) {
	for _, value := range []string{"-1", "0", "65536", "invalid"} {
		t.Setenv("PORT", value)
		if _, err := HTTP(); err == nil {
			t.Fatalf("accepted invalid PORT %q", value)
		}
	}
	for _, c := range []HTTPOptions{{Host: "0.0.0.0", Port: 8080}, {Host: "::", Port: 8080}, {Host: "invalid", Port: 8080}, {Host: "localhost", Port: -1}} {
		if _, err := c.Address(); err == nil {
			t.Fatal("accepted invalid listener configuration")
		}
	}
}
