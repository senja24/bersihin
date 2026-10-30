package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

type fakeWin struct{ restored, focused atomic.Int32 }

func (f *fakeWin) Restore() { f.restored.Add(1) }
func (f *fakeWin) Focus()   { f.focused.Add(1) }

func TestWindowRefRaiseBeforeAndAfterSet(t *testing.T) {
	var r windowRef
	r.raise() // no window yet: must not panic
	w := &fakeWin{}
	r.set(w)
	r.raise()
	if w.restored.Load() != 1 || w.focused.Load() != 1 {
		t.Fatalf("restored=%d focused=%d", w.restored.Load(), w.focused.Load())
	}
}

// Run with -race: set and raise from different goroutines.
func TestWindowRefConcurrent(t *testing.T) {
	var r windowRef
	w := &fakeWin{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.set(w) }()
	go func() { defer wg.Done(); r.raise() }()
	wg.Wait()
}
