// Package fleet schedules independent EVA accounts. Each booking session has
// exactly one owner, while different accounts may serve concurrent searches.
package fleet

import (
	"awarddesk/internal/award"
	"awarddesk/internal/outlook"
	"awarddesk/internal/providers/eva"
	"awarddesk/internal/proxies"
	"awarddesk/internal/storage"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"
)

type airline interface {
	Search(context.Context, award.Query, award.Reporter) (award.Result, error)
	Ensure(context.Context, award.Reporter) error
	CachedSearch(award.Query) (award.Result, bool)
	SessionInfo() eva.SessionInfo
	Close()
}
type entry struct {
	provider    airline
	fingerprint [32]byte
	busy        bool
	last        time.Time
}
type Status struct {
	ID             string          `json:"id"`
	State          string          `json:"state"`
	Stage          string          `json:"stage,omitempty"`
	Message        string          `json:"message,omitempty"`
	CheckedAt      time.Time       `json:"checkedAt,omitempty"`
	NextAttempt    time.Time       `json:"nextAttempt,omitempty"`
	NeedsAttention bool            `json:"needsAttention"`
	Session        eva.SessionInfo `json:"session"`
	Busy           bool            `json:"busy"`
	Queued         bool            `json:"queued"`
}
type Fleet struct {
	db             *storage.Store
	pool           *proxies.Pool
	mu             sync.Mutex
	entries        map[string]*entry
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	build          func(storage.Account, map[string]string) airline
	closed         bool
	pending        map[string]bool
	registrations  int
	prepareQueue   []prepareTask
	prepareWake    chan struct{}
	prepareWorkers int
}

func New(db *storage.Store) *Fleet {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Fleet{db: db, entries: map[string]*entry{}, pending: map[string]bool{}, ctx: ctx, cancel: cancel, prepareWake: make(chan struct{}, 16)}
	f.pool = proxies.NewSource(func() (string, error) { m, e := db.Settings(); return m["PROXY_LIST"], e })
	f.build = func(a storage.Account, m map[string]string) airline {
		ttl, _ := time.ParseDuration(m["EVA_RESULT_CACHE_TTL"])
		if m["FLIGHT_CACHE_ENABLED"] == "false" {
			ttl = 0
		}
		age, _ := time.ParseDuration(m["EVA_SESSION_MAX_AGE"])
		return eva.New(eva.Config{Email: a.Email, Password: a.Password, HyperKey: m["HYPER_API_KEY"], AnyKey: m["ANYSOLVER_API_KEY"], ImpervaSolver: m["EVA_IMPERVA_SOLVER"], Pool: f.pool, Mailbox: outlook.New(outlook.Config{Email: a.OutlookEmail, ClientID: a.ClientID, RefreshToken: a.RefreshToken, StateBackend: db}), ResultCacheTTL: ttl, SessionMaxAge: age, StateBackend: db})
	}
	return f
}
func (f *Fleet) Capabilities() award.Capabilities { return eva.New(eva.Config{}).Capabilities() }

// Cooldowns are per account; one rejected login must not pause the whole fleet.
func (f *Fleet) ManagesCooldowns() bool { return true }
func (f *Fleet) Ready() bool {
	a, e := f.db.Accounts()
	if e != nil {
		return false
	}
	m, e := f.db.Settings()
	if e != nil || m["HYPER_API_KEY"] == "" || m["ANYSOLVER_API_KEY"] == "" || m["PROXY_LIST"] == "" {
		return false
	}
	for _, v := range a {
		if configured(v) {
			return true
		}
	}
	return false
}
func configured(a storage.Account) bool {
	return a.Enabled && a.Email != "" && a.Password != "" && a.OutlookEmail != "" && a.ClientID != "" && a.RefreshToken != ""
}
func (f *Fleet) providerLocked(a storage.Account, m map[string]string) *entry {
	b, _ := json.Marshal(struct {
		A                                          storage.Account
		Hyper, Any, Solver, TTL, Age, CacheEnabled string
	}{a, m["HYPER_API_KEY"], m["ANYSOLVER_API_KEY"], m["EVA_IMPERVA_SOLVER"], m["EVA_RESULT_CACHE_TTL"], m["EVA_SESSION_MAX_AGE"], m["FLIGHT_CACHE_ENABLED"]})
	h := sha256.Sum256(b)
	e := f.entries[a.ID]
	if e == nil {
		e = &entry{}
		f.entries[a.ID] = e
	}
	if !e.busy && (e.provider == nil || e.fingerprint != h) {
		if e.provider != nil {
			e.provider.Close()
		}
		e.provider = f.build(a, m)
		e.fingerprint = h
	}
	return e
}
func (f *Fleet) CachedSearch(q award.Query) (award.Result, bool) {
	if q.Refresh {
		return award.Result{}, false
	}
	accounts, e := f.db.Accounts()
	if e != nil {
		return award.Result{}, false
	}
	m, e := f.db.Settings()
	if e != nil {
		return award.Result{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return award.Result{}, false
	}
	for _, a := range accounts {
		if !configured(a) {
			continue
		}
		en := f.providerLocked(a, m)
		if r, ok := en.provider.CachedSearch(q); ok {
			return r, true
		}
	}
	return award.Result{}, false
}
func (f *Fleet) Statuses() ([]Status, error) {
	accounts, e := f.db.Accounts()
	if e != nil {
		return nil, e
	}
	m, e := f.db.Settings()
	if e != nil {
		return nil, e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Status{}
	for _, a := range accounts {
		en := f.providerLocked(a, m)
		st := Status{ID: a.ID, State: "idle"}
		f.db.RuntimeValue("account:"+a.ID, &st)
		st.ID = a.ID
		st.Busy = en.busy
		st.Queued = f.pending[a.ID] && !st.Busy
		st.Session = en.provider.SessionInfo()
		if st.Busy && st.State != "preparing" {
			st.State = "searching"
		}
		if st.Queued {
			st.State = "queued"
			st.Stage = "queued preparation"
			st.Message = "Waiting for an available session worker"
			st.NeedsAttention = false
			st.NextAttempt = time.Time{}
		}
		if !a.Enabled {
			st.State = "disabled"
		}
		out = append(out, st)
	}
	return out, nil
}

// claim selects ready accounts first and rotates equally ready accounts by use.
func (f *Fleet) claim(ctx context.Context, want string, excluded map[string]bool, force bool) (storage.Account, airline, func(), error) {
	for {
		if e := ctx.Err(); e != nil {
			return storage.Account{}, nil, nil, e
		}
		accounts, err := f.db.Accounts()
		if err != nil {
			return storage.Account{}, nil, nil, err
		}
		m, err := f.db.Settings()
		if err != nil {
			return storage.Account{}, nil, nil, err
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return storage.Account{}, nil, nil, errors.New("fleet is closed")
		}
		limit := award.ConcurrencyLimit(m["SEARCH_CONCURRENCY"])
		active := f.registrations
		for _, en := range f.entries {
			if en.busy {
				active++
			}
		}
		type candidate struct {
			account storage.Account
			entry   *entry
			ready   bool
		}
		var candidates []candidate
		eligible := false
		for _, a := range accounts {
			if !configured(a) || excluded[a.ID] || (want != "" && want != a.ID) {
				continue
			}
			st := Status{}
			f.db.RuntimeValue("account:"+a.ID, &st)
			if !force && (st.NeedsAttention || time.Now().Before(st.NextAttempt)) {
				continue
			}
			eligible = true
			en := f.providerLocked(a, m)
			if en.busy || active >= limit {
				continue
			}
			candidates = append(candidates, candidate{a, en, en.provider.SessionInfo().Ready})
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].ready != candidates[j].ready {
				return candidates[i].ready
			}
			return candidates[i].entry.last.Before(candidates[j].entry.last)
		})
		for _, candidate := range candidates {
			chosen, best := candidate.account, candidate.entry
			unlock, e := f.db.LockState("account:" + chosen.ID)
			if e != nil {
				continue
			}
			best.busy = true
			f.mu.Unlock()
			var once sync.Once
			return chosen, best.provider, func() {
				once.Do(func() { unlock(); f.mu.Lock(); best.busy = false; best.last = time.Now(); f.mu.Unlock() })
			}, nil
		}

		f.mu.Unlock()
		if !eligible {
			return storage.Account{}, nil, nil, award.Failure("accounts_unavailable", "No enabled EVA account is ready; check account status in Admin")
		}
		select {
		case <-ctx.Done():
			return storage.Account{}, nil, nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}
func (f *Fleet) outcome(id string, p airline, err error) {
	st := Status{ID: id, State: "ready", CheckedAt: time.Now().UTC(), Session: p.SessionInfo()}
	if err != nil && !errors.Is(err, context.Canceled) {
		st.State = "failed"
		st.Message = "Session preparation or search failed"
		st.NextAttempt = time.Now().Add(5 * time.Minute)
		var ae *award.Error
		if errors.As(err, &ae) {
			st.Stage = ae.Stage
			st.Message = ae.Message
			st.NeedsAttention = (ae.OutcomeUnknown && ae.Stage != "miles pricing") || ae.Code == "credentials" || ae.Code == "otp" || ae.Code == "login_response" || ae.Code == "login_unverified" || ae.Stage == "credentials"
			if ae.Stage == "miles pricing" {
				st.NextAttempt = time.Time{}
				st.State = "ready"
			}
			if ae.RetryAfterSeconds > 300 {
				st.NextAttempt = time.Now().Add(time.Duration(ae.RetryAfterSeconds) * time.Second)
			}
		}
	}
	_ = f.db.Runtime("account:"+id, st)
}
func (f *Fleet) Search(ctx context.Context, q award.Query, report award.Reporter) (award.Result, error) {
	if r, ok := f.CachedSearch(q); ok {
		return r, nil
	}
	excluded := map[string]bool{}
	var result award.Result
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		a, p, release, e := f.claim(ctx, "", excluded, false)
		if e != nil {
			if err != nil {
				return result, err
			}
			return result, e
		}
		excluded[a.ID] = true
		status := Status{ID: a.ID}
		f.db.RuntimeValue("account:"+a.ID, &status)
		status.State = "searching"
		func() {
			defer release()
			defer func() {
				if recover() != nil {
					err = &award.Error{Code: "internal", Message: "EVA operation stopped unexpectedly; inspect the account before retrying", OutcomeUnknown: true}
				}
				f.outcome(a.ID, p, err)
			}()
			result, err = p.Search(ctx, q, func(progress award.Progress) {
				status.Stage = progress.Stage
				status.Message = progress.Message
				_ = f.db.Runtime("account:"+a.ID, status)
				if report != nil {
					report(progress)
				}
			})
		}()
		if err == nil {
			return result, nil
		}
		var ae *award.Error
		if !errors.As(err, &ae) || !safeFailover(ae) || len(result.Itineraries) > 0 {
			return result, err
		}
		accounts, _ := f.db.Accounts()
		remaining := false
		for _, other := range accounts {
			if configured(other) && !excluded[other.ID] {
				remaining = true
			}
		}
		if !remaining {
			return result, err
		}
		if report != nil {
			report(award.Progress{Stage: "session failover", Message: "Trying another account after interrupted session preparation", Recovery: &award.Recovery{Stage: ae.Stage, Code: ae.Code, Attempt: attempt + 1}})
		}
	}
	return result, err
}
func safeFailover(e *award.Error) bool {
	if e.OutcomeUnknown {
		return false
	}
	switch e.Stage {
	case "pricing", "fare pricing", "traveller details", "mileage pricing", "credentials", "otp":
		return false
	}
	switch e.Code {
	case "session_denied", "session_expired", "session_unverified", "proxy_pool", "proxy_changed", "proxy_connection", "connection", "timeout", "forbidden", "challenge_service", "busy", "http_error", "site_unavailable":
		return e.Stage == "" || e.Stage == "session preparation" || e.Stage == "proxy verification" || e.Stage == "session verification" || e.Stage == "booking security" || e.Stage == "flight search" || e.Stage == "booking handoff" || e.Stage == "booking form security" || e.Stage == "availability"
	}
	return false
}
func (f *Fleet) Ensure(ctx context.Context, report award.Reporter) (err error) {
	a, p, release, e := f.claim(ctx, "", nil, true)
	if e != nil {
		return e
	}
	defer release()
	defer func() {
		if recover() != nil {
			err = &award.Error{Code: "internal", Message: "EVA session preparation stopped unexpectedly", OutcomeUnknown: true}
		}
		f.outcome(a.ID, p, err)
	}()
	return p.Ensure(ctx, report)
}
func (f *Fleet) Prepare(ctx context.Context, id string, report award.Reporter, force bool) (err error) {
	a, p, release, e := f.claim(ctx, id, nil, force)
	if e != nil {
		return e
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = &award.Error{Code: "internal", Message: "EVA session preparation stopped unexpectedly", OutcomeUnknown: true}
		}
		f.outcome(a.ID, p, err)
	}()
	st := Status{ID: a.ID, State: "preparing", CheckedAt: time.Now().UTC()}
	_ = f.db.Runtime("account:"+a.ID, st)
	err = p.Ensure(ctx, func(pr award.Progress) {
		st.Stage = pr.Stage
		st.Message = pr.Message
		_ = f.db.Runtime("account:"+a.ID, st)
		if report != nil {
			report(pr)
		}
	})
	return err
}
func (f *Fleet) Start() {
	// Only work that had not started is safe to recover automatically.
	if rows, e := f.db.Accounts(); e == nil {
		for _, a := range rows {
			var st Status
			f.db.RuntimeValue("account:"+a.ID, &st)
			if configured(a) && st.State == "queued" && st.Stage == "queued preparation" {
				_ = f.schedulePrepare(a.ID, true)
			}
		}
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			f.maintain()
			select {
			case <-f.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (f *Fleet) maintain() {
	_ = f.db.Prune()
	if f.db.Setting("EVA_BACKGROUND_ENABLED") == "false" || !f.Ready() {
		return
	}
	statuses, e := f.Statuses()
	if e != nil {
		return
	}
	// Prepare at most two accounts in parallel, leaving capacity for searches.
	limit, _ := strconv.Atoi(f.db.Setting("SEARCH_CONCURRENCY"))
	if limit < 1 {
		limit = 4
	}
	backgroundLimit := limit / 2
	if backgroundLimit < 1 {
		backgroundLimit = 1
	}
	if backgroundLimit > 2 {
		backgroundLimit = 2
	}
	for _, st := range statuses {
		if st.Busy || st.State == "disabled" || st.NeedsAttention || time.Now().Before(st.NextAttempt) {
			continue
		}
		if st.Session.Ready && time.Since(st.CheckedAt) < 5*time.Minute {
			continue
		}
		f.mu.Lock()
		count := len(f.pending)
		f.mu.Unlock()
		if count >= backgroundLimit {
			return
		}
		_ = f.schedulePrepare(st.ID, false)
	}

}

// Edit excludes active searches and maintenance, including other local processes.
func (f *Fleet) Edit(id string, fn func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if en := f.entries[id]; f.pending[id] || (en != nil && en.busy) {
		return errors.New("account is busy; wait for its search to finish")
	}
	release, e := f.db.LockState("account:" + id)
	if e != nil {
		return e
	}
	defer release()
	if e := fn(); e != nil {
		return e
	}
	accounts, e := f.db.Accounts()
	if e != nil {
		return e
	}
	found := false
	for _, a := range accounts {
		if a.ID == id {
			found = true
		}
	}
	if !found {
		if en := f.entries[id]; en != nil {
			en.provider.Close()
			delete(f.entries, id)
		}
	}
	return nil
}
func (f *Fleet) Close() {
	f.mu.Lock()
	f.closed = true
	f.cancel()
	f.mu.Unlock()
	f.wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, en := range f.entries {
		if en.provider != nil {
			en.provider.Close()
		}
	}
}

func (f *Fleet) RequestPrepare(id string) error { return f.schedulePrepare(id, true) }
func (f *Fleet) schedulePrepare(id string, force bool) error {
	_, err := f.enqueuePrepare(id, force)
	return err
}
