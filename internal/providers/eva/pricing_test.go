package eva

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestMilesCashUnavailableIsNotSelectableAward(t *testing.T) {
	raw := json.RawMessage(`{"isAvailable":false,"maxRankAvailable":{"milesAmount":16800.0,"nonConvertedAmount":{"amount":3040.0}}}`)
	q, err := parseMilesCash(raw, "TWD")
	if err != nil {
		t.Fatal(err)
	}
	if q.Miles != 16800 || q.RemainingCash.Amount != "3040.0" || q.Selectable {
		t.Fatal("backend maximum confused with selectable offer")
	}
	for _, raw := range []string{`{}`, `{"maxRankAvailable":{"milesAmount":-1}}`, `{"maxRankAvailable":{"milesAmount":12.5,"nonConvertedAmount":{"amount":2}}}`} {
		if _, err := parseMilesCash(json.RawMessage(raw), "TWD"); err == nil {
			t.Fatal("invalid or missing quote accepted")
		}
	}
}
func TestLoginJavaScriptFields(t *testing.T) {
	f := form{values: url.Values{"ctl00$content$wuc_login$hid_url": {""}, "ctl00$content$wuc_login$hid_DateTime": {""}}}
	if err := prepareLoginForm(&f, LoginURL, time.Date(2026, 10, 20, 14, 46, 42, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if f.values.Get(f.field("$hid_DateTime")) != "2026/10/20 22:46:42" || f.values.Get(f.field("$hid_url")) != LoginURL {
		t.Fatal("missing browser-generated values")
	}
}

func TestMaximumRequiresAccountSelectableRank(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ranks string
		want  bool
	}{
		{"balance supports only a smaller offset", `[{"milesAmount":8400,"nonConvertedAmount":{"amount":4301}}]`, false},
		{"no selectable ranks returned", `[]`, false},
		{"different cash remainder", `[{"milesAmount":16800,"nonConvertedAmount":{"amount":3200}}]`, false},
		{"explicit maximum rank", `[{"milesAmount":16800,"nonConvertedAmount":{"amount":3040}}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"isAvailable":true,"maxRankAvailable":{"milesAmount":16800,"nonConvertedAmount":{"amount":3040}},"listRanks":` + tc.ranks + `}`)
			q, err := parseMilesCash(raw, "TWD")
			if err != nil {
				t.Fatal(err)
			}
			if q.Selectable != tc.want {
				t.Fatalf("selectable = %v, want %v", q.Selectable, tc.want)
			}
		})
	}
}
func TestLoginFeedbackExcludesScriptsAndInputs(t *testing.T) {
	body := []byte(`<div id="wuc_Error">Wrong CAPTCHA entry. <input value="secret"><script>secret</script>123456</div>`)
	text := loginFeedback(body)
	if text != "Wrong CAPTCHA entry. ######" {
		t.Fatalf("unsafe or incorrect diagnostic: %q", text)
	}
}
