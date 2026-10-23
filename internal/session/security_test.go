package session

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestProductionRejectsUntrustedTLS(t *testing.T) {
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached a server with an untrusted certificate")
	}))
	target.Config.ErrorLog = log.New(io.Discard, "", 0)
	target.StartTLS()
	defer target.Close()
	u, _ := url.Parse(target.URL)
	client, err := New("", []string{u.Host})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := client.Do(ctx, "POST", target.URL, nil, nil); err == nil {
		t.Fatal("accepted an untrusted TLS certificate")
	}
}

func TestProxyInputHygiene(t *testing.T) {
	for _, input := range []string{
		"http://user:secret%0D%0Aheader@proxy.example:8000",
		"http://us%00er:secret@proxy.example:8000",
		"bad host:8000:user:secret",
	} {
		if _, err := ParseProxy(input); err == nil {
			t.Fatal("accepted malformed proxy input")
		}
	}
	got, err := ParseProxy("  proxy.example:8000:user:pass  \r\n")
	if err != nil || got != "http://user:pass@proxy.example:8000" {
		t.Fatal("surrounding clipboard whitespace was not normalized")
	}
}
