package antibot

import "testing"

func TestAdaptiveScriptLayout(t *testing.T) {
	body := []byte(`<script type="text/javascript" src="https://example.test/public/script" async></script><script defer src="/fresh/script/path/token" async></script>`)
	path, err := parseSensorPath(body)
	if err != nil || path != "/fresh/script/path/token" {
		t.Fatalf("adaptive layout not recognized: %v", err)
	}
	for _, body := range []string{
		`<script src="//evil.test/a/b/c" async></script>`,
		`<script src="/a/b/c"></script><script src="/d/e/f"></script>`,
		`<script src="/a/b/c?forward=external"></script>`,
	} {
		if _, err := parseSensorPath([]byte(body)); err == nil {
			t.Fatal("ambiguous or external script accepted")
		}
	}
}
