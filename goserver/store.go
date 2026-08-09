package main

import (
	"sync"
	"time"
)

type entry struct {
	val []byte
	exp time.Time
}

type Store struct {
	mu         sync.RWMutex
	m          map[string]entry
	ttl        time.Duration
	maxEntries int
	maxBytes   int
	usedBytes  int
}

func NewStore(ttl time.Duration, maxEntries int) *Store {
	return NewStoreWithByteLimit(ttl, maxEntries, 0)
}

func NewStoreWithByteLimit(ttl time.Duration, maxEntries, maxBytes int) *Store {
	s := &Store{m: make(map[string]entry), ttl: ttl, maxEntries: maxEntries, maxBytes: maxBytes}
	go s.janitor()
	return s
}

func (s *Store) Get(key string) ([]byte, bool) {
	now := time.Now()
	s.mu.RLock()
	e, ok := s.m[key]
	if !ok {
		s.mu.RUnlock()
		return nil, false
	}
	if now.Before(e.exp) {
		s.mu.RUnlock()
		return e.val, true
	}
	s.mu.RUnlock()

	s.mu.Lock()
	if current, exists := s.m[key]; exists && !now.Before(current.exp) {
		delete(s.m, key)
		s.usedBytes -= len(current.val)
	}
	s.mu.Unlock()
	return nil, false
}

func (s *Store) Set(key string, val []byte) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.m[key]
	currentSize := len(current.val)
	if s.maxBytes > 0 && s.usedBytes-currentSize+len(val) > s.maxBytes {
		for candidate, existing := range s.m {
			if candidate != key && !now.Before(existing.exp) {
				delete(s.m, candidate)
				s.usedBytes -= len(existing.val)
			}
		}
		if s.usedBytes-currentSize+len(val) > s.maxBytes {
			return false
		}
	}
	if !exists && len(s.m) >= s.maxEntries {
		for candidate, existing := range s.m {
			if !now.Before(existing.exp) {
				delete(s.m, candidate)
				s.usedBytes -= len(existing.val)
			}
		}
		if len(s.m) >= s.maxEntries {
			// ponytail: random eviction avoids LRU bookkeeping. Add LRU only if
			// production access patterns prove random eviction harmful.
			for candidate := range s.m {
				s.usedBytes -= len(s.m[candidate].val)
				delete(s.m, candidate)
				break
			}
		}
	}
	s.usedBytes += len(val) - currentSize
	s.m[key] = entry{val: val, exp: now.Add(s.ttl)}
	return true
}

func (s *Store) janitor() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for key, existing := range s.m {
			if !now.Before(existing.exp) {
				delete(s.m, key)
				s.usedBytes -= len(existing.val)
			}
		}
		s.mu.Unlock()
	}
}
