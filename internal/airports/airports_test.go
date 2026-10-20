package airports

import "testing"

func TestAirportSearch(t *testing.T) {
	a, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if len(a) < 8000 {
		t.Fatal("airport dataset incomplete")
	}
	for _, q := range []string{"copenhagen", "CPH", "cph"} {
		r := Search(a, q)
		if len(r) == 0 || r[0].Code != "CPH" {
			t.Fatalf("failed search %s", q)
		}
	}
	if len(Search(a, "L")) != 0 {
		t.Fatal("short search not limited")
	}
	if len(Search(a, "international")) > 12 {
		t.Fatal("result limit ignored")
	}
}
