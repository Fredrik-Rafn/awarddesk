package integration

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/jobs"
)

func TestSearchPollingReturnsNotModifiedUntilSnapshotChanges(t *testing.T) {
	s, handler := adminTestServer(t)
	if err := s.Jobs.SetPersistence(s.Admin.DB); err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{ID: "revision-test", Status: "completed", UpdatedAt: time.Now().UTC(), Result: &award.Result{Complete: true}}
	save := func() {
		data, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Admin.DB.SaveJob(job.ID, data); err != nil {
			t.Fatal(err)
		}
	}
	save()
	request := func(tag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost:8080/api/searches/"+job.ID, nil)
		if tag != "" {
			r.Header.Set("If-None-Match", tag)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	first := request("")
	if first.Code != 200 || first.Header().Get("ETag") == "" {
		t.Fatal("initial result has no revision")
	}
	tag := first.Header().Get("ETag")
	unchanged := request(tag)
	if unchanged.Code != 304 || unchanged.Body.Len() != 0 {
		t.Fatal("unchanged result retransmitted", unchanged.Code)
	}
	job.UpdatedAt = job.UpdatedAt.Add(time.Second)
	job.Result.Warnings = []string{"updated"}
	save()
	updated := request(tag)
	if updated.Code != 200 || updated.Header().Get("ETag") == tag {
		t.Fatal("new revision suppressed")
	}
}
