package fleet

import (
	"context"
	"testing"
	"time"
)

func TestRegistrationSharesWorkerLimitAndExclusiveProxy(t *testing.T) {
	f, _ := setup(t, 1)
	f.db.SetSettings(map[string]string{"SEARCH_CONCURRENCY": "2", "PROXY_LIST": "http://core-residential.evomi.com:1000:user:secret_session-abcdefghij\nhttp://core-residential.evomi.com:1000:user:secret_session-klmnopqrst"})
	a, releaseA, e := f.RegistrationLease(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer releaseA()
	b, releaseB, e := f.RegistrationLease(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if a.Template == b.Template || a.URL == b.URL {
		t.Fatal("parallel signups reused a proxy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, release, e := f.claim(ctx, "a", nil, true); e == nil {
		release()
		t.Fatal("search exceeded shared worker limit")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if _, release, e := f.RegistrationLease(ctx2); e == nil {
		release()
		t.Fatal("registration exceeded worker limit")
	}
	releaseB()
	_, _, release, e := f.claim(context.Background(), "a", nil, true)
	if e != nil {
		t.Fatal(e)
	}
	release()
	c, releaseC, e := f.RegistrationLease(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer releaseC()
	if c.URL == b.URL {
		t.Fatal("fresh signup reused previous sticky identity")
	}
}
