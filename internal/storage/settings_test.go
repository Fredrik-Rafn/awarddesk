package storage

import (
	"path/filepath"
	"testing"
)

func TestRetiredConfigurationRemovedWithoutLosingEVAOrHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetSettings(map[string]string{"OBSOLETE_PASSWORD": "retired", "HYPER_API_KEY": "keep", "_IMPORTED": "existing"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{".state/obsolete/session.json", ".state/eva/session.json", ".state/outlook/token.json", ".state/eva-results/result.json"} {
		if err = db.WriteState(key, []byte(`{"valid":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SaveJob("history", []byte(`{"id":"history","query":{"provider":"eva"},"status":"completed"}`)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Setting("OBSOLETE_PASSWORD") != "" || db.Setting("HYPER_API_KEY") != "keep" || db.Setting("_IMPORTED") != "existing" {
		t.Fatal("configuration cleanup removed active values or retained obsolete ones")
	}
	states, err := db.States()
	if err != nil || len(states) != 3 {
		t.Fatalf("states=%d error=%v", len(states), err)
	}
	if _, err = db.LoadJob("history"); err != nil {
		t.Fatal("configuration cleanup lost historical search")
	}
}
