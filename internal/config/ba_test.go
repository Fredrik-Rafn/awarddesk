package config

import (
	"strings"
	"testing"
)

func TestBAConfigurationRequiresAccountAndBothSolvers(t *testing.T) {
	settings := map[string]string{"BA_EMAIL": "test@example.com", "BA_PASSWORD": "test-password", "HYPER_API_KEY": "test-hyper", "ANYSOLVER_API_KEY": "test-any"}
	for _, missing := range []string{"", "BA_EMAIL", "BA_PASSWORD", "HYPER_API_KEY", "ANYSOLVER_API_KEY"} {
		t.Run("missing="+missing, func(t *testing.T) {
			values := make(map[string]string, len(settings))
			for key, value := range settings {
				if key != missing {
					values[key] = value
				}
			}
			provider, ready, err := BAFromSettings(values)
			if err != nil || provider == nil || ready != (missing == "") {
				t.Fatalf("ready=%t error=%v", ready, err)
			}
		})
	}
}

func TestBAConfigurationRejectsProxyWithoutLeakingCredentials(t *testing.T) {
	_, _, err := BAFromSettings(map[string]string{"AWARDDESK_PROXY": "ftp://user:private-password@localhost:1000"})
	if err == nil || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("invalid proxy accepted or credentials exposed: %v", err)
	}
}
