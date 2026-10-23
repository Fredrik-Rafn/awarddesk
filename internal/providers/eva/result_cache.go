package eva

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"awarddesk/internal/award"
	"awarddesk/internal/sessionstore"
)

const resultCacheVersion = 4

type cachedResult struct {
	Version int          `json:"version"`
	Result  award.Result `json:"result"`
}

func (p *Provider) resultStore(q award.Query) sessionstore.Store {
	q.Refresh = false
	data, _ := json.Marshal(q)
	h := sha256.Sum256(append([]byte(p.identity()+"\x00"), data...))
	return sessionstore.Store{Backend: p.config.StateBackend, Path: filepath.Join(".state", "eva-results", hex.EncodeToString(h[:])+".json")}
}

func (p *Provider) CachedSearch(q award.Query) (award.Result, bool) {
	if p.config.ResultCacheTTL <= 0 || q.Refresh {
		return award.Result{}, false
	}
	if data, err := p.resultStore(q).Load(); err == nil {
		var saved cachedResult
		if json.Unmarshal(data, &saved) == nil && (saved.Version == resultCacheVersion || (q.Mode == "" && saved.Version > 0 && saved.Version < resultCacheVersion && (saved.Version != 1 || q.Adults == 1))) && validCachedResult(saved.Result, q, time.Now(), p.config.ResultCacheTTL) {
			saved.Result.Cached = true
			expires := saved.Result.FetchedAt.Add(p.config.ResultCacheTTL)
			saved.Result.CacheExpiresAt = &expires
			return milesEligibleResult(saved.Result), true
		}
	}
	return award.Result{}, false
}

func (p *Provider) searchCached(ctx context.Context, q award.Query, report award.Reporter, run func(context.Context, award.Query, award.Reporter) (award.Result, error)) (award.Result, error) {
	if err := q.Validate(time.Now()); err != nil {
		return award.Result{Query: q}, err
	}
	if err := ctx.Err(); err != nil {
		return award.Result{Query: q}, err
	}
	refresh := q.Refresh
	q.Refresh = false
	store := p.resultStore(q)
	if !refresh {
		if result, ok := p.CachedSearch(q); ok {
			if report != nil {
				report(award.Progress{Stage: "cached results", Message: "Reusing a recent complete EVA search"})
			}
			return result, nil
		}
	}
	result, err := run(ctx, q, report)
	result = milesEligibleResult(result)
	if err == nil && p.config.ResultCacheTTL > 0 && validCachedResult(result, q, time.Now(), p.config.ResultCacheTTL) && ctx.Err() == nil {
		expires := result.FetchedAt.Add(p.config.ResultCacheTTL)
		result.CacheExpiresAt = &expires
		data, marshalErr := json.Marshal(cachedResult{Version: resultCacheVersion, Result: result})
		if marshalErr == nil {
			if saveErr := store.Save(data); saveErr != nil {
				slog.Warn("EVA result cache write failed")
			} else {
				if store.Backend == nil {
					pruneResultCache(filepath.Dir(store.Path), 100)
				}
			}
		}
	}
	return result, err
}

func milesEligibleResult(result award.Result) award.Result {
	result = award.CloneResult(result)
	flights := make([]award.Itinerary, 0, len(result.Itineraries))
	for _, item := range result.Itineraries {
		fares := make([]award.Fare, 0, len(item.Fares))
		for _, fare := range item.Fares {
			if fare.MilesCashEligible == nil || *fare.MilesCashEligible {
				fares = append(fares, fare)
			}
		}
		if len(fares) > 0 {
			item.Fares = fares
			flights = append(flights, item)
		}
	}
	result.Itineraries = flights
	return result
}

func validCachedResult(r award.Result, q award.Query, now time.Time, ttl time.Duration) bool {
	return r.Complete && r.Query == q && !r.FetchedAt.IsZero() && !r.FetchedAt.After(now) && now.Sub(r.FetchedAt) < ttl
}

func pruneResultCache(dir string, limit int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type entry struct {
		path     string
		modified time.Time
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, entry{filepath.Join(dir, e.Name()), info.ModTime()})
		}
	}
	for len(files) > limit {
		oldest := 0
		for i := range files {
			if files[i].modified.Before(files[oldest].modified) {
				oldest = i
			}
		}
		_ = os.Remove(files[oldest].path)
		files = append(files[:oldest], files[oldest+1:]...)
	}
}
