// Package progress throttles scan progress and tracks cancellable scans.
package progress

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

// Sink receives progress updates.
type Sink func(model.ScanProgress)

// Reporter is what scanners call. A nil Reporter discards everything.
type Reporter func(phase string, done, total int, current string)

// Throttle returns a Reporter for the run id of task that forwards at most one update per
// config.ProgressInterval. A change of phase and a finished phase (done ==
// total > 0, or the phase "Done") always pass, so the page never misses a
// step or the end. It is safe for concurrent use.
func Throttle(sink Sink, id string, task model.ScanTask) Reporter {
	return throttle(sink, id, task, time.Now, config.ProgressInterval)
}

func throttle(sink Sink, id string, task model.ScanTask, now func() time.Time, every time.Duration) Reporter {
	if sink == nil {
		return func(string, int, int, string) {}
	}
	var (
		mu    sync.Mutex
		last  time.Time
		phase string
		sent  int // highest done sent for this phase
	)
	return func(p string, done, total int, current string) {
		// The sink is called under the lock, so updates reach the page in the
		// order they were let through.
		mu.Lock()
		defer mu.Unlock()
		t := now()
		// an update that overtook a later one in flight is stale: drop it
		if p == phase && done < sent {
			return
		}
		pass := p != phase || p == "Done" || (total > 0 && done >= total) || t.Sub(last) >= every
		if pass {
			phase, last, sent = p, t, done
			sink(model.ScanProgress{ID: id, Task: task, Phase: p, Done: done, Total: total, Current: current})
		}
	}
}

// Call is nil-safe.
func (r Reporter) Call(phase string, done, total int, current string) {
	if r != nil {
		r(phase, done, total, current)
	}
}

// ErrCancelled is what a scan returns when the page cancelled it.
var ErrCancelled = errors.New("scan cancelled")

// Errors Begin returns. The texts reach the page.
var (
	// ErrBadID: the run id is not 16 to 64 letters, digits, "-" or "_".
	ErrBadID = errors.New("Invalid request")
	// ErrDuplicateID: a run with this id is already going.
	ErrDuplicateID = errors.New("Invalid request: run id in use")
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// ValidID reports whether id is an acceptable run id. The page makes them up
// (see src/lib/runId.ts); Go only checks them.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Scans tracks the running scans and operations by run id so the page can
// cancel each one. MyGo cancels a call's context only when the page navigates
// away or its window closes; a Cancel button needs this registry. Two runs of
// the same task (for example the space map of two disks) are independent.
type Scans struct {
	mu   sync.Mutex
	next uint64
	run  map[string]entry
}

type entry struct {
	seq    uint64
	cancel context.CancelFunc
}

// Begin starts the run with this id. It fails for an invalid id or one that
// is already running. Call the returned func when the run ends.
func (s *Scans) Begin(ctx context.Context, runID string, task model.ScanTask) (context.Context, func(), error) {
	if !ValidID(runID) {
		return ctx, func() {}, ErrBadID
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.run == nil {
		s.run = map[string]entry{}
	}
	if _, busy := s.run[runID]; busy {
		s.mu.Unlock()
		cancel()
		return ctx, func() {}, ErrDuplicateID
	}
	s.next++
	seq := s.next
	s.run[runID] = entry{seq: seq, cancel: cancel}
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		if cur, ok := s.run[runID]; ok && cur.seq == seq {
			delete(s.run, runID)
		}
		s.mu.Unlock()
		cancel()
	}, nil
}

// Running reports whether a run with this id is going.
func (s *Scans) Running(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.run[runID]
	return ok
}

// Cancel stops the run with this id, if any, and reports whether there was one.
func (s *Scans) Cancel(runID string) bool {
	s.mu.Lock()
	e, ok := s.run[runID]
	s.mu.Unlock()
	if ok {
		e.cancel()
	}
	return ok
}

// Err maps a context error of a cancelled scan to ErrCancelled.
func Err(ctx context.Context, err error) error {
	if err == nil && ctx.Err() == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ErrCancelled
	}
	return err
}
