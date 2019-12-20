package utils

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// MapLock use to make sure that only one operates the container at the same time.
type MapLock struct {
	mutex sync.Mutex
	ids   map[string]struct{}
}

// NewMapLock returns map lock struct.
func NewMapLock() *MapLock {
	return &MapLock{
		ids: make(map[string]struct{}),
	}
}

// Trylock will try to get lock with id.
func (l *MapLock) Trylock(id string) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	_, ok := l.ids[id]
	if !ok {
		l.ids[id] = struct{}{}
		return true
	}
	return false
}

// Unlock unlock.
func (l *MapLock) Unlock(id string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	delete(l.ids, id)
}

// TrylockWithRetry will try to get lock with timeout by id.
func (l *MapLock) TrylockWithRetry(ctx context.Context, id string) bool {
	var retry = 32

	for {
		ok := l.Trylock(id)
		if ok {
			return true
		}

		// sleep random duration by retry
		select {
		case <-time.After(time.Millisecond * time.Duration(rand.Intn(retry))):
			if retry < 2048 {
				retry = retry << 1
			}
			continue
		case <-ctx.Done():
			return false
		}
	}
}
