package integration

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAdminHistoryStatisticsAndExport(t *testing.T) {
	s, h := adminTestServer(t)
	cookie := adminLogin(t, s, h)
	now := time.Now().UTC()
	for i := 0; i < 127; i++ {
		status := "completed"
		if i%2 == 0 {
			status = "failed"
		}
		data := map[string]any{"id": fmt.Sprintf("history-%03d", i), "query": map[string]any{"provider": "eva", "origin": "TPE", "destination": "HKG", "date": "2026-11-30", "cabin": "business", "adults": 1}, "status": status, "createdAt": now.Add(-time.Duration(i+1) * time.Minute), "updatedAt": now.Add(-time.Duration(i+1) * time.Minute).Add(20 * time.Second), "result": map[string]any{"itineraries": []any{}, "complete": true}, "recoveries": []any{}}
		b, _ := json.Marshal(data)
		if e := s.Admin.DB.SaveJob(data["id"].(string), b); e != nil {
			t.Fatal(e)
		}
	}
	for _, path := range []string{"/api/admin/history", "/api/admin/analytics", "/api/admin/export", "/api/admin/search?id=history-001"} {
		if w := adminRequest(h, s, "GET", path, "", nil, true); w.Code != 401 {
			t.Fatalf("unprotected endpoint %s", path)
		}
	}
	w := adminRequest(h, s, "GET", "/api/admin/history?range=all&page=3&limit=50", "", cookie, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var page struct {
		Total int
		Items []map[string]any
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || page.Total != 127 || len(page.Items) != 27 {
		t.Fatal("history pagination truncated records")
	}
	if strings.Contains(w.Body.String(), "\"result\"") {
		t.Fatal("history includes result payloads")
	}
	w = adminRequest(h, s, "GET", "/api/admin/analytics?range=all", "", cookie, true)
	var stats struct {
		Total, Completed, Failed int
		SuccessRate              float64
	}
	if e := json.Unmarshal(w.Body.Bytes(), &stats); w.Code != 200 || e != nil || stats.Total != 127 || stats.Completed != 63 || stats.Failed != 64 {
		t.Fatalf("statistics differ from full history: %+v", stats)
	}
	w = adminRequest(h, s, "GET", "/api/admin/export?range=all", "", cookie, true)
	rows, e := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if w.Code != 200 || e != nil || len(rows) != 128 {
		t.Fatalf("export truncated or malformed: status=%d rows=%d error=%v", w.Code, len(rows), e)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("export isn't a download")
	}
	w = adminRequest(h, s, "GET", "/api/admin/search?id=history-001", "", cookie, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"result\"") {
		t.Fatal("detail view missing result")
	}
	for _, path := range []string{"/api/admin/history?status=unknown", "/api/admin/history?sort=bad", "/api/admin/analytics?range=99d", "/api/admin/export?mode=invalid"} {
		if w = adminRequest(h, s, "GET", path, "", cookie, true); w.Code != 400 {
			t.Fatalf("invalid filter accepted: %s", path)
		}
	}
}
