package main

import "sync"

// raiser is the part of *mygo.Window a second launch needs.
type raiser interface {
	Restore()
	Focus()
}

// windowRef shares the main window between WhenReady, which creates it, and
// OnSecondInstance, which may run on another goroutine at any time.
type windowRef struct {
	mu sync.Mutex
	w  raiser
}

func (r *windowRef) set(w raiser) {
	r.mu.Lock()
	r.w = w
	r.mu.Unlock()
}

// raise brings the window to the front, or does nothing before it exists.
func (r *windowRef) raise() {
	r.mu.Lock()
	w := r.w
	r.mu.Unlock()
	if w != nil {
		w.Restore()
		w.Focus()
	}
}
