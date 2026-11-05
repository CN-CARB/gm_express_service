package main

import (
	"container/list"
	"sync"
	"time"
)

type entry struct {
	val  []byte
	exp  time.Time
	node *list.Element
}

type Store struct {
	mu         sync.RWMutex
	m          map[string]entry
	lru        list.List // front is most recently used; values are map keys
	ttl        time.Duration
	maxEntries int
	maxBytes   int
	usedBytes  int
}

func NewStore(ttl time.Duration, maxEntries, maxBytes int) *Store {
	s := &Store{m: make(map[string]entry), ttl: ttl, maxEntries: maxEntries, maxBytes: maxBytes}
	go s.janitor()
	return s
}

func (s *Store) Get(key string) ([]byte, bool) {
	// A successful read updates recency, but never extends the original TTL.
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return nil, false
	}
	if !time.Now().Before(e.exp) {
		s.remove(key)
		return nil, false
	}
	s.lru.MoveToFront(e.node)
	return e.val, true
}

func (s *Store) Set(key string, val []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// An impossible insertion must not evict existing data, including this key.
	if s.maxEntries <= 0 || (s.maxBytes > 0 && len(val) > s.maxBytes) {
		return false
	}
	now := time.Now()
	s.remove(key)
	if len(s.m) >= s.maxEntries || (s.maxBytes > 0 && s.usedBytes+len(val) > s.maxBytes) {
		// Expired entries may be anywhere in LRU order. Reclaim them before
		// evicting any live entry, even one less recently used than they are.
		s.deleteExpired(now)
	}
	for len(s.m) >= s.maxEntries || (s.maxBytes > 0 && s.usedBytes+len(val) > s.maxBytes) {
		s.remove(s.lru.Back().Value.(string))
	}
	s.m[key] = entry{val: val, exp: now.Add(s.ttl), node: s.lru.PushFront(key)}
	s.usedBytes += len(val)
	return true
}

// remove and deleteExpired require the caller to hold s.mu exclusively.
func (s *Store) remove(key string) {
	if e, ok := s.m[key]; ok {
		s.lru.Remove(e.node)
		s.usedBytes -= len(e.val)
		delete(s.m, key)
	}
}

func (s *Store) deleteExpired(now time.Time) {
	for key, existing := range s.m {
		if !now.Before(existing.exp) {
			s.remove(key)
		}
	}
}

func (s *Store) janitor() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		s.deleteExpired(now)
		s.mu.Unlock()
	}
}
