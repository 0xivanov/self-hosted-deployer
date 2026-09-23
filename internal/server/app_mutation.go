package server

import (
	"context"
	"sync"
)

// One server process serializes mutations per application. This is not a
// distributed operation fence and is not evidence for a lost-reply recovery.
type appMutationLocks struct {
	mu   sync.Mutex
	apps map[string]*appMutationLock
}
type appMutationLock struct {
	gate  chan struct{}
	users int
}

func (l *appMutationLocks) acquire(ctx context.Context, app string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.apps == nil {
		l.apps = map[string]*appMutationLock{}
	}
	lock := l.apps[app]
	if lock == nil {
		lock = &appMutationLock{gate: make(chan struct{}, 1)}
		l.apps[app] = lock
	}
	lock.users++
	l.mu.Unlock()
	forget := func() {
		l.mu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(l.apps, app)
		}
		l.mu.Unlock()
	}
	select {
	case lock.gate <- struct{}{}:
		release := func() { <-lock.gate; forget() }
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}
