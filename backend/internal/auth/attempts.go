package auth

import (
	"sync"
	"time"
)

const (
	// After this many wrong passwords for one account inside the window, more
	// tries are refused until the window ends.
	maxWrong    = 10
	wrongWindow = 15 * time.Minute
	// Past this many names being counted, the ones whose window has ended are dropped.
	sweepAbove = 10_000
)

// attempts counts wrong passwords per account, to slow guessing. It lives in
// memory: there is one API instance, and forgetting the counts on a restart
// costs nothing.
type attempts struct {
	mu    sync.Mutex
	wrong map[string]wrong
}

type wrong struct {
	n     int
	since time.Time
}

// blocked reports whether key has used up its tries for now.
func (a *attempts) blocked(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.wrong[key]
	return ok && now.Sub(w.since) < wrongWindow && w.n >= maxWrong
}

// failed counts one wrong password for key.
func (a *attempts) failed(key string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wrong == nil {
		a.wrong = map[string]wrong{}
	}
	if len(a.wrong) > sweepAbove {
		for k, w := range a.wrong {
			if now.Sub(w.since) >= wrongWindow {
				delete(a.wrong, k)
			}
		}
	}
	w := a.wrong[key]
	if now.Sub(w.since) >= wrongWindow {
		w = wrong{since: now}
	}
	w.n++
	a.wrong[key] = w
}

// passed forgets key's wrong tries after a right one.
func (a *attempts) passed(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.wrong, key)
}
