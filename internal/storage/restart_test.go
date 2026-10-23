package storage

import (
	"fmt"
	"testing"

	"awarddesk/internal/jobs"
)

func TestRestartInterruptsEveryUnfinishedJobBeyondHistoryLimit(t *testing.T) {
	db := testDB(t)
	tx, err := db.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 2300; i++ {
		status := "completed"
		if i < 1200 {
			status = "queued"
			if i%2 == 0 {
				status = "running"
			}
		}
		id := fmt.Sprintf("job-%04d", i)
		if _, err = tx.Exec("INSERT INTO jobs VALUES(?,?,?)", id, []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, id, status)), i); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Jobs()
	if err != nil || len(rows) != 2200 {
		t.Fatalf("loaded %d rows, error %v; expected every active row plus 1000 recent finished rows", len(rows), err)
	}
	service := jobs.New()
	if err = service.SetPersistence(db); err != nil {
		service.Close()
		t.Fatal(err)
	}
	service.Close()
	var active, interrupted, completed int
	if err = db.DB.QueryRow(`SELECT count(*) FROM jobs WHERE json_extract(data,'$.status') IN ('queued','running')`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRow(`SELECT count(*) FROM jobs WHERE json_extract(data,'$.status')='interrupted'`).Scan(&interrupted); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRow(`SELECT count(*) FROM jobs WHERE json_extract(data,'$.status')='completed'`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if active != 0 || interrupted != 1200 || completed != 1100 {
		t.Fatalf("active=%d interrupted=%d completed=%d", active, interrupted, completed)
	}
}
