package web

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"awarddesk/internal/storage"
)

func adminSince(value string, now time.Time) (time.Time, bool) {
	switch value {
	case "24h":
		return now.Add(-24 * time.Hour), true
	case "1h":
		return now.Add(-time.Hour), true
	case "7d":
		return now.Add(-7 * 24 * time.Hour), true
	case "30d":
		return now.Add(-30 * 24 * time.Hour), true
	case "", "all":
		return time.Time{}, true
	}
	return time.Time{}, false
}
func (s *Server) adminHistory(w http.ResponseWriter, r *http.Request) {
	a := s.Admin
	q := r.URL.Query()
	since, ok := adminSince(q.Get("range"), time.Now())
	if !ok {
		fail(w, 400, "Unknown time range")
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	filter := storage.HistoryFilter{Text: q.Get("q"), Status: q.Get("status"), Provider: q.Get("provider"), Mode: q.Get("mode"), Sort: q.Get("sort"), Since: since, Page: page, Limit: limit}
	result, e := a.DB.SearchHistory(filter)
	if e != nil {
		fail(w, 400, "Invalid history filters or history unavailable")
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) adminAnalytics(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	since, ok := adminSince(r.URL.Query().Get("range"), now)
	if !ok {
		fail(w, 400, "Unknown time range")
		return
	}
	v, e := s.Admin.DB.SearchAnalytics(since, now)
	if e != nil {
		fail(w, 500, "Could not read search statistics")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) adminSearchDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if len(id) > 80 {
		fail(w, 400, "Invalid search ID")
		return
	}
	b, e := s.Admin.DB.LoadJob(id)
	if e != nil {
		fail(w, 404, "Search not found")
		return
	}
	writeJSON(w, 200, json.RawMessage(b))
}

// Prefix spreadsheet control characters so exported diagnostics stay inert.
func csvCell(v string) string {
	if strings.ContainsAny(strings.TrimLeft(v, " "), "\t\r\n") {
		v = strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " ")
	}
	trimmed := strings.TrimLeft(v, " ")
	if trimmed != "" && strings.ContainsRune("=+-@\t\r", rune(trimmed[0])) {
		return "'" + v
	}
	return v
}
func (s *Server) adminExport(w http.ResponseWriter, r *http.Request) {
	since, ok := adminSince(r.URL.Query().Get("range"), time.Now())
	if !ok {
		fail(w, 400, "Unknown time range")
		return
	}
	q := r.URL.Query()
	f := storage.HistoryFilter{Text: q.Get("q"), Status: q.Get("status"), Provider: q.Get("provider"), Mode: q.Get("mode"), Sort: q.Get("sort"), Since: since, Page: 1, Limit: 100}
	first, e := s.Admin.DB.SearchHistory(f)
	if e != nil {
		fail(w, 400, "Invalid export filters")
		return
	}
	if first.Total > 100000 {
		fail(w, 400, "Narrow the filters to export fewer than 100,000 searches")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="awarddesk-searches.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"Search ID", "Provider", "Origin", "Destination", "Departure date", "Cabin", "Status", "Source", "Created UTC", "Updated UTC", "Wall duration ms", "Flights", "Recovery events", "Error code", "Error stage"})
	e = s.Admin.DB.ExportSearchHistory(f, func(v storage.SearchRow) error {
		if e := r.Context().Err(); e != nil {
			return e
		}

		source := "live"
		if v.Cached {
			source = "cached"
		}
		code, stage := "", ""
		if v.Error != nil {
			code, stage = v.Error.Code, v.Error.Stage
		}
		row := []string{v.ID, v.Query.Provider, v.Query.Origin, v.Query.Destination, v.Query.Date, string(v.Query.Cabin), v.Status, source, v.CreatedAt.UTC().Format(time.RFC3339), v.UpdatedAt.UTC().Format(time.RFC3339), strconv.FormatInt(v.DurationMS, 10), strconv.Itoa(v.Flights), strconv.Itoa(v.Recoveries), code, stage}
		for i := range row {
			row[i] = csvCell(row[i])
		}
		return writer.Write(row)
	})
	if e != nil {
		return
	}

	writer.Flush()
}
