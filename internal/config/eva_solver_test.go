package config

import "testing"

func TestEVASolverSelectionModes(t *testing.T) {
	t.Setenv("AWARDDESK_PROXY_FILE", "")
	t.Setenv("EVA_RESULT_CACHE_TTL", "0s")
	t.Setenv("EVA_EMAIL", "")
	for _, mode := range []string{"", "auto", "hyper", "anysolver"} {
		t.Setenv("EVA_IMPERVA_SOLVER", mode)
		if _, _, err := EVA(); err != nil {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
	}
	t.Setenv("EVA_IMPERVA_SOLVER", "unknown")
	if _, _, err := EVA(); err == nil {
		t.Fatal("unknown solver accepted")
	}
}
