package progress

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
)

func TestThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	var got []model.ScanProgress
	r := throttle(func(p model.ScanProgress) { got = append(got, p) }, runA, model.TaskJunk, func() time.Time { return now }, 120*time.Millisecond)

	step := func(d time.Duration, phase string, done, total int) {
		now = now.Add(d)
		r(phase, done, total, "")
	}
	step(0, "A", 0, 3)                     // first of a phase passes
	step(10*time.Millisecond, "A", 0, 3)   // dropped
	step(50*time.Millisecond, "A", 0, 3)   // dropped (60ms since last pass)
	step(70*time.Millisecond, "A", 1, 3)   // passes (130ms)
	step(1*time.Millisecond, "B", 1, 3)    // phase change passes
	step(1*time.Millisecond, "B", 3, 3)    // finished passes
	step(1*time.Millisecond, "Done", 0, 0) // Done passes
	if len(got) != 5 {
		t.Fatalf("got %d updates, want 5: %+v", len(got), got)
	}
	for _, p := range got {
		if p.ID != runA {
			t.Fatalf("progress without the run id: %+v", p)
		}
	}
	if got[0].Task != model.TaskJunk || got[3].Phase != "B" || got[4].Phase != "Done" {
		t.Fatalf("unexpected updates: %+v", got)
	}
}

func TestNilSinkAndReporter(t *testing.T) {
	Throttle(nil, runA, model.TaskJunk)("x", 0, 0, "")
	var r Reporter
	r.Call("x", 0, 0, "")
}

const (
	runA = "AAAAAAAAAAAAAAAA"
	runB = "BBBBBBBBBBBBBBBB"
)

func begin(t *testing.T, s *Scans, ctx context.Context, id string, task model.ScanTask) (context.Context, func()) {
	t.Helper()
	c, end, err := s.Begin(ctx, id, task)
	if err != nil {
		t.Fatal(err)
	}
	return c, end
}

func TestScansCancel(t *testing.T) {
	var s Scans
	ctx, end := begin(t, &s, context.Background(), runA, model.TaskLarge)
	defer end()
	if s.Cancel(runB) {
		t.Fatal("cancelled a scan that is not running")
	}
	if !s.Cancel(runA) {
		t.Fatal("running scan not found")
	}
	if ctx.Err() == nil {
		t.Fatal("context not cancelled")
	}
	if Err(ctx, nil) != ErrCancelled {
		t.Fatal("Err should map to ErrCancelled")
	}
}

// M6: two runs of the same task are independent; cancelling one leaves the other.
func TestTwoRunsOfOneTaskAreIndependent(t *testing.T) {
	var s Scans
	first, endFirst := begin(t, &s, context.Background(), runA, model.TaskSpaces)
	second, endSecond := begin(t, &s, context.Background(), runB, model.TaskSpaces)
	if first.Err() != nil || second.Err() != nil {
		t.Fatal("a second run must not cancel the first")
	}
	if !s.Cancel(runA) {
		t.Fatal("first run not found")
	}
	if first.Err() == nil {
		t.Fatal("first run should be cancelled")
	}
	if second.Err() != nil {
		t.Fatal("second run must survive")
	}
	endFirst() // must not unregister the second
	if !s.Cancel(runB) {
		t.Fatal("second run lost its registration")
	}
	endSecond()
	if s.Cancel(runB) || s.Cancel(runA) {
		t.Fatal("ended runs must be gone")
	}
}

func TestBeginRefusesBadAndDuplicateIDs(t *testing.T) {
	var s Scans
	for _, id := range []string{"", "short", "has space 123456789012", "bad/char/12345678901", "a.b.c.d.e.f.g.h.i.j.k", "ünicode-1234567890123", strings.Repeat("a", 65), "trailing-newline-1234\n"} {
		if _, _, err := s.Begin(context.Background(), id, model.TaskJunk); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	for _, id := range []string{runA, strings.Repeat("a", 64), "abc_DEF-123456789"} {
		_, end, err := s.Begin(context.Background(), id, model.TaskJunk)
		if err != nil {
			t.Errorf("refused %q: %v", id, err)
		}
		end()
	}
	_, end := begin(t, &s, context.Background(), runA, model.TaskJunk)
	if _, _, err := s.Begin(context.Background(), runA, model.TaskApps); err == nil {
		t.Fatal("a running id was accepted twice")
	}
	end()
	if _, end2, err := s.Begin(context.Background(), runA, model.TaskApps); err != nil {
		t.Fatalf("an ended id is free again: %v", err)
	} else {
		end2()
	}
}

func TestParentCancel(t *testing.T) {
	var s Scans
	parent, stop := context.WithCancel(context.Background())
	ctx, end := begin(t, &s, parent, runA, model.TaskApps)
	defer end()
	stop()
	if ctx.Err() == nil {
		t.Fatal("child should follow parent")
	}
}

func TestThrottleKeepsOrderUnderConcurrency(t *testing.T) {
	var mu sync.Mutex
	var got []int
	r := Throttle(func(p model.ScanProgress) {
		mu.Lock() // the sink itself is not serialised by the test
		got = append(got, p.Done)
		mu.Unlock()
	}, runA, model.TaskDupes)
	var wg sync.WaitGroup
	var next atomic.Int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				// "total > 0 && done >= total" lets every update through
				r("Comparing files", int(next.Add(1)), 1, "")
			}
		}()
	}
	wg.Wait()
	if len(got) == 0 || len(got) > 1600 {
		t.Fatalf("got %d updates", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("update %d (%d) arrived after %d", i, got[i], got[i-1])
		}
	}
}
