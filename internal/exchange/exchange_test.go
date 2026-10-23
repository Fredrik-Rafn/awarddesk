package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awarddesk/internal/award"
)

type memoryBackend struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (b *memoryBackend) ReadState(path string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.values[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), v...), nil
}
func (b *memoryBackend) WriteState(path string, v []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.values == nil {
		b.values = map[string][]byte{}
	}
	b.values[path] = append([]byte(nil), v...)
	return nil
}
func (b *memoryBackend) DeleteState(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.values, path)
	return nil
}
func (b *memoryBackend) LockState(string) (func(), error) { return func() {}, nil }

func rateResponse(now time.Time) map[string]any {
	return map[string]any{"result": "success", "base_code": "USD", "time_last_update_unix": now.Add(-time.Hour).Unix(), "time_next_update_unix": now.Add(23 * time.Hour).Unix(), "rates": map[string]any{"USD": json.Number("1"), "TWD": json.Number("32.591824"), "EUR": json.Number("0.8")}}
}

func TestConcurrentFailedRefreshBacksOffAndRecovers(t *testing.T) {
	when := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	var available atomic.Bool
	s := newTestService(t, when, &memoryBackend{}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !available.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(rateResponse(when))
	})
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if value, err := s.USD(context.Background(), award.Money{Amount: "3047", Currency: "TWD"}); err == nil || value != nil {
				t.Error("failed refresh returned a conversion")
			}
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("failed endpoint called %d times for concurrent callers", calls.Load())
	}
	available.Store(true)
	when = when.Add(31 * time.Second)
	s.now = func() time.Time { return when }
	if _, err := s.USD(context.Background(), award.Money{Amount: "3047", Currency: "TWD"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one recovery request, calls=%d", calls.Load())
	}
}
func newTestService(t *testing.T, when time.Time, backend *memoryBackend, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s := New(backend)
	s.url = server.URL
	s.now = func() time.Time { return when }
	return s
}

func TestUSDExactConversionsAndNativeRounding(t *testing.T) {
	when := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	s := newTestService(t, when, &memoryBackend{}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credentials sent to rate provider")
		}
		json.NewEncoder(w).Encode(rateResponse(when))
	})
	for _, tt := range []struct{ amount, currency, want string }{{"3047", "TWD", "93.49"}, {"12188", "TWD", "373.96"}, {"0", "TWD", "0.00"}, {"0.004", "USD", "0.00"}, {"0.005", "USD", "0.01"}, {"1.005", "USD", "1.01"}, {"1.004999999999999999999999999999999", "USD", "1.00"}, {"0.008", "EUR", "0.01"}} {
		t.Run(tt.amount+tt.currency, func(t *testing.T) {
			got, err := s.USD(context.Background(), award.Money{Amount: tt.amount, Currency: tt.currency})
			if err != nil || got.Amount != tt.want || got.Currency != "USD" {
				t.Fatalf("got %+v %v want %s", got, err, tt.want)
			}
			if tt.currency == "USD" {
				if got.Source != "native" || got.Rate != "1" || !got.AsOf.IsZero() {
					t.Fatalf("invalid native metadata %+v", got)
				}
			} else if got.Source != source || got.AsOf != when.Add(-time.Hour) || got.Rate == "" {
				t.Fatalf("invalid FXmetadata %+v", got)
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatalf("requests=%d want1", calls.Load())
	}
	for _, money := range []award.Money{{Amount: "-1", Currency: "USD"}, {Amount: "NaN", Currency: "TWD"}, {Amount: "1", Currency: "twd"}, {Amount: "1e99", Currency: "TWD"}} {
		if _, err := s.USD(context.Background(), money); err == nil {
			t.Fatalf("invalid money accepted %+v", money)
		}
	}
}

func TestUSDDailyCachePersistsAndRefreshes(t *testing.T) {
	when := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	backend := &memoryBackend{}
	var calls atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(rateResponse(when))
	}
	s := newTestService(t, when, backend, handler)
	if _, err := s.USD(context.Background(), award.Money{Amount: "3047", Currency: "TWD"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ReadState(".state/exchange/usd.json"); err != nil {
		t.Fatal("rate cache not persisted")
	}
	second := newTestService(t, when, backend, handler)
	if _, err := second.USD(context.Background(), award.Money{Amount: "3047", Currency: "TWD"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("new service ignored persisted daily rates")
	}
	when = when.Add(24 * time.Hour)
	second.now = func() time.Time { return when }
	if _, err := second.USD(context.Background(), award.Money{Amount: "3047", Currency: "TWD"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("next update did not refresh rates")
	}
}

func TestUSDSingleFetchUnderConcurrency(t *testing.T) {
	when := time.Now().UTC()
	var calls atomic.Int32
	s := newTestService(t, when, &memoryBackend{}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(rateResponse(when))
	})
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.USD(context.Background(), award.Money{Amount: "3047", Currency: "TWD"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests=%d", calls.Load())
	}
}

func TestUSDCancellationWhileWaitingOrFetching(t *testing.T) {
	when := time.Now().UTC()
	started := make(chan struct{})
	s := newTestService(t, when, &memoryBackend{}, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })
	s.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.USD(ctx, award.Money{Amount: "1", Currency: "TWD"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	<-s.gate
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.USD(ctx, award.Money{Amount: "1", Currency: "TWD"}); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fetch: %v", err)
	}
}

func TestUSDRejectsInvalidOrStaleRateResponses(t *testing.T) {
	when := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"failed response", func(p map[string]any) { p["result"] = "error" }},
		{"wrong base", func(p map[string]any) { p["base_code"] = "EUR" }},
		{"future asof", func(p map[string]any) { p["time_last_update_unix"] = when.Add(time.Second).Unix() }},
		{"older than48h", func(p map[string]any) { p["time_last_update_unix"] = when.Add(-49 * time.Hour).Unix() }},
		{"past nextupdate", func(p map[string]any) { p["time_next_update_unix"] = when.Unix() }},
		{"next beforelast", func(p map[string]any) { p["time_next_update_unix"] = when.Add(-2 * time.Hour).Unix() }},
		{"next too distant", func(p map[string]any) { p["time_next_update_unix"] = when.Add(72 * time.Hour).Unix() }},
		{"USDnot1", func(p map[string]any) { p["rates"].(map[string]any)["USD"] = 2 }},
		{"negative rate", func(p map[string]any) { p["rates"].(map[string]any)["TWD"] = -1 }},
		{"zero rate", func(p map[string]any) { p["rates"].(map[string]any)["TWD"] = 0 }},
		{"huge rate", func(p map[string]any) { p["rates"].(map[string]any)["TWD"] = json.Number("1e20") }},
		{"nonfinite rate", func(p map[string]any) { p["rates"].(map[string]any)["TWD"] = "NaN" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := rateResponse(when)
			tt.change(p)
			raw, _ := json.Marshal(p)
			if _, err := decode(raw, when); err == nil {
				t.Fatal("invalid rate payload accepted")
			}
		})
	}
	if _, err := decode([]byte(`{`), when); err == nil {
		t.Fatal("malformed json accepted")
	}
}

func TestUSDBoundedBodyAndNoRedirects(t *testing.T) {
	for _, mode := range []string{"oversize", "redirect", "status"} {
		t.Run(mode, func(t *testing.T) {
			when := time.Now().UTC()
			var calls atomic.Int32
			s := newTestService(t, when, &memoryBackend{}, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "oversize":
					w.Write([]byte(strings.Repeat(" ", maxBody+1)))
				case "redirect":
					http.Redirect(w, r, "/redirected", http.StatusFound)
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			})
			if _, err := s.USD(context.Background(), award.Money{Amount: "1", Currency: "TWD"}); err == nil {
				t.Fatal("invalid HTTPresponse accepted")
			}
			if calls.Load() != 1 {
				t.Fatal("redirect followed")
			}
		})
	}
	s := New(&memoryBackend{})
	if s.url != endpoint || s.client.Timeout != 6*time.Second || s.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("unsafe default client configuration")
	}
	if _, err := s.USD(context.Background(), award.Money{Amount: "1", Currency: "USD"}); err != nil {
		t.Fatal("nativeUSD attempted network")
	}
}
