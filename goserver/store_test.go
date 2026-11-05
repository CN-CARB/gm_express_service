package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func assertStoreState(t *testing.T, s *Store, wantOrder ...string) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.m) != len(wantOrder) || s.lru.Len() != len(wantOrder) {
		t.Fatalf("map/list sizes = %d/%d, want %d", len(s.m), s.lru.Len(), len(wantOrder))
	}
	bytes := 0
	node := s.lru.Front()
	for _, key := range wantOrder {
		if node == nil || node.Value != key {
			t.Fatalf("unexpected LRU order, want %v", wantOrder)
		}
		e, ok := s.m[key]
		if !ok || e.node != node {
			t.Fatalf("map/list mismatch for %s", key)
		}
		bytes += len(e.val)
		node = node.Next()
	}
	if s.usedBytes != bytes {
		t.Fatalf("byte accounting = %d, want %d", s.usedBytes, bytes)
	}
}

func Test_Store_LRU_read_and_write_promote(t *testing.T) {
	s := NewStore(time.Hour, 3, 0)
	s.Set("A", []byte("A"))
	s.Set("B", []byte("B"))
	s.Set("C", []byte("C"))
	s.Get("A")
	s.Get("missing")
	assertStoreState(t, s, "A", "C", "B")
	s.Set("D", []byte("D"))
	assertStoreState(t, s, "D", "A", "C")
	if _, ok := s.Get("B"); ok {
		t.Fatal("least recently used entry survived")
	}
	s.Set("C", []byte("updated"))
	assertStoreState(t, s, "C", "D", "A")
	s.Set("E", nil)
	assertStoreState(t, s, "E", "C", "D")
}

func Test_Store_LRU_byte_budget_evicts_multiple_entries(t *testing.T) {
	s := NewStore(time.Hour, 10, 10)
	s.Set("A", []byte("123"))
	s.Set("B", []byte("456"))
	s.Set("C", []byte("789"))
	s.Get("A")
	if !s.Set("D", []byte("1234567")) {
		t.Fatal("in-budget item was rejected")
	}
	assertStoreState(t, s, "D", "A")
	s.Set("A", []byte("12345678"))
	assertStoreState(t, s, "A")
	s.Set("A", []byte("1"))
	assertStoreState(t, s, "A")
}

func Test_Store_oversized_insert_or_overwrite_preserves_contents(t *testing.T) {
	s := NewStore(time.Hour, 2, 3)
	s.Set("A", []byte("12"))
	s.Set("B", []byte("3"))
	for _, key := range []string{"A", "C"} {
		if s.Set(key, []byte("1234")) {
			t.Fatal("oversized value was accepted")
		}
		assertStoreState(t, s, "B", "A")
	}
	if value, _ := s.Get("A"); string(value) != "12" {
		t.Fatal("failed overwrite changed original value")
	}
}

func Test_Store_read_does_not_extend_TTL(t *testing.T) {
	s := NewStore(time.Hour, 2, 0)
	s.Set("A", []byte("payload"))
	s.mu.RLock()
	deadline := s.m["A"].exp
	s.mu.RUnlock()
	s.Get("A")
	s.mu.RLock()
	unchanged := s.m["A"].exp.Equal(deadline)
	s.mu.RUnlock()
	if !unchanged {
		t.Fatal("read extended TTL")
	}
	expireStoreEntry(s, "A")
	if _, ok := s.Get("A"); ok {
		t.Fatal("read resurrected expired entry")
	}
	assertStoreState(t, s)
}

func expireStoreEntry(s *Store, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[key]
	e.exp = time.Now().Add(-time.Second)
	s.m[key] = e
}

func Test_Store_expiration_precedes_live_LRU_eviction(t *testing.T) {
	for _, budget := range []int{0, 2} {
		s := NewStore(time.Hour, 2, budget)
		s.Set("live", []byte("1"))
		s.Set("expired", []byte("2"))
		expireStoreEntry(s, "expired")
		s.Set("new", []byte("3"))
		assertStoreState(t, s, "new", "live")
		expireStoreEntry(s, "new")
		s.mu.Lock()
		s.deleteExpired(time.Now())
		s.mu.Unlock()
		assertStoreState(t, s, "live")
	}
}

func Test_Store_token_entries_are_LRU_even_with_zero_payload_bytes(t *testing.T) {
	s := NewStore(24*time.Hour, 2, 0)
	s.Set("server", nil)
	s.Set("client", nil)
	if _, ok := s.Get("server"); !ok {
		t.Fatal("nil payload must still be a valid token")
	}
	s.Set("new", nil)
	assertStoreState(t, s, "new", "server")
}

func Test_Store_concurrent_access_preserves_invariants(t *testing.T) {
	s := NewStore(time.Hour, 20, 100)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for j := 0; j < 200; j++ {
				key := fmt.Sprintf("%d-%d", worker, j%10)
				s.Set(key, []byte("12345"))
				s.Get(key)
				s.Get("missing")
			}
		}(i)
	}
	workers.Wait()
	var keys []string
	s.mu.RLock()
	for node := s.lru.Front(); node != nil; node = node.Next() {
		keys = append(keys, node.Value.(string))
	}
	if s.usedBytes > 100 || len(s.m) > 20 {
		t.Fatal("capacity exceeded")
	}
	s.mu.RUnlock()
	assertStoreState(t, s, keys...)
}

func Test_HTTP_size_lookup_promotes_file(t *testing.T) {
	s := NewStore(time.Hour, 2, 0)
	handler, registration := registeredHandler(t, s)
	s.Set("A", []byte("A"))
	s.Set("B", []byte("B"))
	response := request(t, handler, "GET", "/v1/size/"+registration.Client+"/A", "", "")
	assertResponse(t, response, 200, `{"size":"1"}`)
	s.Set("C", []byte("C"))
	assertStoreState(t, s, "C", "A")
}
