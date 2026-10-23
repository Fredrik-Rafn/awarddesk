package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"awarddesk/internal/award"
)

type SearchRow struct {
	ID         string         `json:"id"`
	Query      award.Query    `json:"query"`
	Status     string         `json:"status"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	Progress   award.Progress `json:"progress"`
	Error      *award.Error   `json:"error,omitempty"`
	Cached     bool           `json:"cached"`
	Flights    int            `json:"flights"`
	Recoveries int            `json:"recoveryCount"`
	DurationMS int64          `json:"durationMs"`
}
type HistoryFilter struct {
	Text, Status, Provider, Mode, Sort string
	Since                              time.Time
	Page, Limit                        int
}
type HistoryPage struct {
	Items []SearchRow `json:"items"`
	Total int         `json:"total"`
	Page  int         `json:"page"`
	Limit int         `json:"limit"`
}

const searchProjection = `json_remove(data,'$.result','$.recoveries'),COALESCE(json_extract(data,'$.result.cached'),0),COALESCE(json_array_length(data,'$.result.itineraries'),0),COALESCE(json_array_length(data,'$.recoveries'),0)`

func historyWhere(f HistoryFilter) (string, []any, error) {
	where := "1=1"
	args := []any{}
	if len(f.Text) > 100 {
		return "", nil, errors.New("Search text must be at most 100 characters")
	}
	if f.Text != "" {
		where += ` AND instr(lower(COALESCE(json_extract(data,'$.query.origin'),'')||' '||COALESCE(json_extract(data,'$.query.destination'),'')||' '||COALESCE(json_extract(data,'$.query.date'),'')||' '||COALESCE(json_extract(data,'$.query.cabin'),'')||' '||id),?)>0`
		args = append(args, strings.ToLower(strings.TrimSpace(f.Text)))
	}
	if f.Status != "" {
		switch f.Status {
		case "completed", "failed", "running", "queued", "cancelled", "interrupted":
		default:
			return "", nil, errors.New("Unknown search status")
		}
		where += ` AND json_extract(data,'$.status')=?`
		args = append(args, f.Status)
	}
	if f.Provider != "" {
		if f.Provider != "eva" {
			return "", nil, errors.New("Unknown provider")
		}
		where += ` AND json_extract(data,'$.query.provider')=?`
		args = append(args, f.Provider)
	}
	switch f.Mode {
	case "":
	case "cached":
		where += ` AND json_extract(data,'$.result.cached')=1`
	case "live":
		where += ` AND COALESCE(json_extract(data,'$.result.cached'),0)=0`
	default:
		return "", nil, errors.New("Unknown result source")
	}
	if !f.Since.IsZero() {
		where += ` AND julianday(json_extract(data,'$.createdAt'))>=julianday(?)`
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	return where, args, nil
}
func scanSearch(rows *sql.Rows) (SearchRow, error) {
	var v SearchRow
	var b []byte
	var cached int
	e := rows.Scan(&b, &cached, &v.Flights, &v.Recoveries)
	if e != nil {
		return v, e
	}
	flights, recoveries := v.Flights, v.Recoveries
	if e = json.Unmarshal(b, &v); e != nil {
		return v, e
	}
	v.Flights, v.Recoveries = flights, recoveries
	v.Cached = cached != 0
	end := v.UpdatedAt
	if v.Status == "running" || v.Status == "queued" {
		end = time.Now()
	}
	v.DurationMS = end.Sub(v.CreatedAt).Milliseconds()
	if v.DurationMS < 0 {
		v.DurationMS = 0
	}
	return v, nil
}
func (s *Store) SearchHistory(f HistoryFilter) (HistoryPage, error) {
	where, args, e := historyWhere(f)
	if e != nil {
		return HistoryPage{}, e
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Page > 100000 {
		f.Page = 100000
	}
	if f.Limit < 1 {
		f.Limit = 25
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	order := `julianday(json_extract(data,'$.createdAt')) DESC,id DESC`
	switch f.Sort {
	case "", "newest":
	case "oldest":
		order = `julianday(json_extract(data,'$.createdAt')) ASC,id ASC`
	case "slowest":
		order = `(julianday(json_extract(data,'$.updatedAt'))-julianday(json_extract(data,'$.createdAt'))) DESC,id DESC`
	default:
		return HistoryPage{}, errors.New("Unknown sort order")
	}

	tx, e := s.DB.Begin()
	if e != nil {
		return HistoryPage{}, e
	}
	defer tx.Rollback()
	out := HistoryPage{Items: []SearchRow{}, Page: f.Page, Limit: f.Limit}
	if e = tx.QueryRow("SELECT COUNT(*) FROM jobs WHERE "+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	pageArgs := append(append([]any{}, args...), f.Limit, (f.Page-1)*f.Limit)
	rows, e := tx.Query("SELECT "+searchProjection+" FROM jobs WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", pageArgs...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, err := scanSearch(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func (s *Store) ExportSearchHistory(f HistoryFilter, visit func(SearchRow) error) error {
	where, args, e := historyWhere(f)
	if e != nil {
		return e
	}
	order := `julianday(json_extract(data,'$.createdAt')) DESC,id DESC`
	switch f.Sort {
	case "", "newest":
	case "oldest":
		order = `julianday(json_extract(data,'$.createdAt')) ASC,id ASC`
	case "slowest":
		order = `(julianday(json_extract(data,'$.updatedAt'))-julianday(json_extract(data,'$.createdAt'))) DESC,id DESC`
	default:
		return errors.New("Unknown sort order")
	}
	rows, e := s.DB.Query("SELECT "+searchProjection+" FROM jobs WHERE "+where+" ORDER BY "+order+" LIMIT 100000", args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanSearch(rows)
		if e != nil {
			return e
		}
		if e = visit(v); e != nil {
			return e
		}
	}
	return rows.Err()
}

type TrendBucket struct {
	At      time.Time `json:"at"`
	Success int       `json:"success"`
	Failed  int       `json:"failed"`
	Cached  int       `json:"cached"`
	Other   int       `json:"other"`
}
type RouteStats struct {
	Route     string `json:"route"`
	Searches  int    `json:"searches"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	AverageMS int64  `json:"averageMs"`
	duration  int64
	timed     int
}
type FailureStats struct {
	Code  string `json:"code"`
	Stage string `json:"stage"`
	Count int    `json:"count"`
}
type Analytics struct {
	Total           int            `json:"total"`
	Completed       int            `json:"completed"`
	Failed          int            `json:"failed"`
	Cancelled       int            `json:"cancelled"`
	Interrupted     int            `json:"interrupted"`
	Running         int            `json:"running"`
	Queued          int            `json:"queued"`
	CacheHits       int            `json:"cacheHits"`
	Recovered       int            `json:"recovered"`
	RetryEvents     int            `json:"retryEvents"`
	LiveCompleted   int            `json:"liveCompleted"`
	LiveFailed      int            `json:"liveFailed"`
	Flights         int            `json:"flights"`
	SuccessRate     *float64       `json:"successRate"`
	LiveSuccessRate *float64       `json:"liveSuccessRate"`
	MedianLiveMS    *int64         `json:"medianLiveMs"`
	P95LiveMS       *int64         `json:"p95LiveMs"`
	Trend           []TrendBucket  `json:"trend"`
	Routes          []RouteStats   `json:"routes"`
	Failures        []FailureStats `json:"failures"`
	Active          []SearchRow    `json:"active"`
	FirstAt         *time.Time     `json:"firstAt"`
	GeneratedAt     time.Time      `json:"generatedAt"`
}

func rate(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	v := 100 * float64(n) / float64(d)
	return &v
}
func (s *Store) SearchAnalytics(since, now time.Time) (Analytics, error) {
	where, args, e := historyWhere(HistoryFilter{Since: since})
	if e != nil {
		return Analytics{}, e
	}
	rows, e := s.DB.Query("SELECT "+searchProjection+" FROM jobs WHERE "+where+" ORDER BY julianday(json_extract(data,'$.createdAt'))", args...)
	if e != nil {
		return Analytics{}, e
	}
	defer rows.Close()
	a := Analytics{Trend: []TrendBucket{}, Routes: []RouteStats{}, Failures: []FailureStats{}, Active: []SearchRow{}, GeneratedAt: now}
	routeMap := map[string]*RouteStats{}
	failureMap := map[string]*FailureStats{}
	var durations []int64
	var series []struct {
		CreatedAt time.Time
		Status    string
		Cached    bool
	}
	for rows.Next() {
		v, err := scanSearch(rows)
		if err != nil {
			return a, err
		}
		a.Total++
		if a.FirstAt == nil {
			at := v.CreatedAt
			a.FirstAt = &at
		}
		series = append(series, struct {
			CreatedAt time.Time
			Status    string
			Cached    bool
		}{v.CreatedAt, v.Status, v.Cached})
		a.RetryEvents += v.Recoveries
		if v.Cached {
			a.CacheHits++
		}
		key := v.Query.Origin + " → " + v.Query.Destination
		r := routeMap[key]
		if r == nil {
			r = &RouteStats{Route: key}
			routeMap[key] = r
		}
		r.Searches++
		switch v.Status {
		case "completed":
			a.Completed++
			r.Completed++
			a.Flights += v.Flights
			if v.Recoveries > 0 {
				a.Recovered++
			}
			if !v.Cached {
				a.LiveCompleted++
				durations = append(durations, v.DurationMS)
				r.duration += v.DurationMS
				r.timed++
			}
		case "failed":
			a.Failed++
			r.Failed++
			if !v.Cached {
				a.LiveFailed++
			}
			if v.Error != nil {
				k := v.Error.Code + "\x00" + v.Error.Stage
				f := failureMap[k]
				if f == nil {
					f = &FailureStats{Code: v.Error.Code, Stage: v.Error.Stage}
					failureMap[k] = f
				}
				f.Count++
			}
		case "cancelled":
			a.Cancelled++
		case "interrupted":
			a.Interrupted++
		case "running":
			a.Running++
			a.Active = append(a.Active, v)
		case "queued":
			a.Queued++
			a.Active = append(a.Active, v)
		}
	}
	if e = rows.Err(); e != nil {
		return a, e
	}
	a.SuccessRate = rate(a.Completed, a.Completed+a.Failed)
	a.LiveSuccessRate = rate(a.LiveCompleted, a.LiveCompleted+a.LiveFailed)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) > 0 {
		median := durations[len(durations)/2]
		if len(durations)%2 == 0 {
			median = (durations[len(durations)/2-1] + median) / 2
		}
		p95 := durations[int(math.Ceil(float64(len(durations))*.95))-1]
		a.MedianLiveMS = &median
		a.P95LiveMS = &p95
	}
	for _, r := range routeMap {
		if r.timed > 0 {
			r.AverageMS = r.duration / int64(r.timed)
		}
		a.Routes = append(a.Routes, *r)
	}
	sort.Slice(a.Routes, func(i, j int) bool {
		if a.Routes[i].Searches == a.Routes[j].Searches {
			return a.Routes[i].Route < a.Routes[j].Route
		}
		return a.Routes[i].Searches > a.Routes[j].Searches
	})
	if len(a.Routes) > 8 {
		a.Routes = a.Routes[:8]
	}
	for _, f := range failureMap {
		a.Failures = append(a.Failures, *f)
	}
	sort.Slice(a.Failures, func(i, j int) bool {
		if a.Failures[i].Count == a.Failures[j].Count {
			return a.Failures[i].Code+a.Failures[i].Stage < a.Failures[j].Code+a.Failures[j].Stage
		}
		return a.Failures[i].Count > a.Failures[j].Count
	})
	if len(a.Failures) > 8 {
		a.Failures = a.Failures[:8]
	}
	start := since
	if start.IsZero() {
		start = now.Add(-24 * time.Hour)
		if a.FirstAt != nil {
			start = *a.FirstAt
		}
	}
	span := now.Sub(start)
	if span < time.Hour {
		span = time.Hour
	}
	step := time.Duration(math.Ceil(float64(span)/float64(24*time.Minute))) * time.Minute
	if step < time.Minute {
		step = time.Minute
	}
	for i := 0; i < 24; i++ {
		a.Trend = append(a.Trend, TrendBucket{At: start.Add(time.Duration(i) * step)})
	}
	for _, v := range series {
		index := int(v.CreatedAt.Sub(start) / step)
		if index < 0 {
			continue
		}
		if index > 23 {
			index = 23
		}
		bucket := &a.Trend[index]
		if v.Cached {
			bucket.Cached++
		} else {
			switch v.Status {
			case "completed":
				bucket.Success++
			case "failed":
				bucket.Failed++
			default:
				bucket.Other++
			}
		}
	}
	return a, nil
}
