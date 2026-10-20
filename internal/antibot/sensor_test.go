package antibot

import (
	"errors"
	"testing"
)

func TestSensorStopSignalAndRequiredInteractionRounds(t *testing.T) {
	for _, tc := range []struct {
		cookie             string
		submitted, minimum int
		stop               bool
	}{
		{"opaque~-1~other~0~", 1, 0, false},
		{"opaque~0~other", 1, 0, true},
		{"opaque~3~other", 2, 0, false},
		{"opaque~3~other", 3, 0, true},
		{"opaque~0~other", 1, 3, false},
		{"opaque~0~other", 2, 3, false},
		{"opaque~0~other", 3, 3, true},
	} {
		if got := sensorMayStop(tc.cookie, tc.submitted, tc.minimum); got != tc.stop {
			t.Fatalf("submitted=%d minimum=%d stop=%v want=%v", tc.submitted, tc.minimum, got, tc.stop)
		}
	}
}

func TestResolveSensorScriptCoexistingChallenges(t *testing.T) {
	body := []byte(`<script type="text/javascript" nonce="fixture" src="/regular"></script><script src="/secondary?v=00000000-0000-0000-0000-000000000001"></script>`)
	calls := 0
	path, err := resolveSensorScript(body, func([]byte) ([]byte, error) {
		calls++
		// The refreshed page may still advertise SBSD. It must not recurse.
		return []byte(`<script type="text/javascript" nonce="fixture" src="/fresh"></script><script src="/secondary?v=00000000-0000-0000-0000-000000000002"></script>`), nil
	})
	if err != nil || path != "/fresh" || calls != 1 {
		t.Fatalf("path=%q calls=%d err=%v", path, calls, err)
	}
	failed := errors.New("challenge failed")
	_, err = resolveSensorScript(body, func([]byte) ([]byte, error) { return nil, failed })
	if !errors.Is(err, failed) {
		t.Fatal("challenge error was ignored in favor of the regular script")
	}
}

func TestResolveSensorScriptWithoutSBSD(t *testing.T) {
	path, err := resolveSensorScript([]byte(`<script type="text/javascript" nonce="fixture" src="/regular"></script>`), func([]byte) ([]byte, error) {
		t.Fatal("solver invoked without a challenge")
		return nil, nil
	})
	if err != nil || path != "/regular" {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestChallengeEndpointUsesAirlineOrigin(t *testing.T) {
	for _, host := range []string{"www.ana.co.jp", "www.britishairways.com"} {
		for _, path := range []string{"/sensor", "https://" + host + "/sensor"} {
			endpoint, origin, err := challengeEndpoint("https://"+host+"/en/eur/", path)
			if err != nil || origin != "https://"+host || endpoint != origin+"/sensor" {
				t.Fatalf("incorrect challenge origin: %q %q %v", endpoint, origin, err)
			}
		}
	}
	for _, path := range []string{"https://other.test/sensor", "//other.test/sensor", "http://www.ana.co.jp/sensor", "https://user@www.ana.co.jp/sensor", "/sensor#fragment"} {
		if _, _, err := challengeEndpoint("https://www.ana.co.jp/en/eur/", path); err == nil {
			t.Fatal("unsafe challenge endpoint accepted")
		}
	}
}
