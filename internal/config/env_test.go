package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnv(t *testing.T) {
	keys := []string{"AWARDDESK_TEST_ENV_A", "AWARDDESK_TEST_ENV_B", "AWARDDESK_TEST_ENV_C"}
	for _, key := range keys {
		old, present := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if present {
				os.Setenv(key, old)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	t.Setenv(keys[2], "existing")
	path := filepath.Join(t.TempDir(), ".env")
	body := "\ufeff# comment\n" + keys[0] + `="secret=#with\\slash"` + "\n" + keys[1] + "='literal$#value'\n" + keys[2] + "=replacement\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(keys[0]) != `secret=#with\slash` || os.Getenv(keys[1]) != "literal$#value" || os.Getenv(keys[2]) != "existing" {
		t.Fatal("incorrect environment parsing or precedence")
	}
	if err := LoadEnv(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidEnvDoesNotLeakValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	for _, content := range []string{`TOKEN="private-value`, `INVALID KEY=private-value`, `private-value`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		err := LoadEnv(path)
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("malformed input accepted or leaked")
		}
	}
}
