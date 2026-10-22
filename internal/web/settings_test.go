package web

import "testing"

func TestEVASolverSettingValidation(t *testing.T) {
	for _, mode := range []string{"auto", "hyper", "anysolver"} {
		if err := validateSetting("EVA_IMPERVA_SOLVER", mode); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
	for _, mode := range []string{"", "unknown"} {
		if err := validateSetting("EVA_IMPERVA_SOLVER", mode); err == nil {
			t.Fatalf("invalid mode %q accepted", mode)
		}
	}
}
