package airports

import (
	"embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed airports.json
var files embed.FS

type Airport struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	City       string `json:"city"`
	Country    string `json:"country"`
	searchText string
	cityLower  string
	codeLower  string
}

func Load() ([]Airport, error) {
	b, e := files.ReadFile("airports.json")
	if e != nil {
		return nil, e
	}
	var a []Airport
	e = json.Unmarshal(b, &a)
	for i := range a {
		a[i].searchText = strings.ToLower(a[i].Code + " " + a[i].City + " " + a[i].Name)
		a[i].cityLower = strings.ToLower(a[i].City)
		a[i].codeLower = strings.ToLower(a[i].Code)
	}
	return a, e
}

func Search(all []Airport, q string) []Airport {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []Airport{}
	if len(q) < 2 || len(q) > 80 {
		return out
	}
	for _, a := range all {
		if strings.Contains(a.searchText, q) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		rank := func(a Airport) int {
			if a.codeLower == q {
				return 0
			}
			if strings.HasPrefix(a.codeLower, q) {
				return 1
			}
			if strings.HasPrefix(a.cityLower, q) {
				return 2
			}
			return 3
		}
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Code < out[j].Code
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}
