// Express Service - self-host edition, protocol-compatible with the Cloudflare/Deno version.
// Routes and response shapes match index.js exactly.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const maxDataSize = 24 * 1024 * 1024

var (
	expiration      = envDuration("GM_EXPRESS_EXPIRATION", 5*time.Minute) // data TTL
	tokenExpiration = 24 * time.Hour
	maxEntries      = envInt("GM_EXPRESS_MAX_ENTRIES", 8192)
)

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return time.Duration(v) * time.Second
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

// --- TTL store: sharded map + janitor. Zero deps, safe for concurrent use. ---

const numShards = 64

type entry struct {
	val []byte
	exp time.Time
}

type shard struct {
	mu sync.Mutex
	m  map[string]entry
}

type Store struct {
	shards   [numShards]shard
	ttl      time.Duration
	capPerSh int
}

func NewStore(ttl time.Duration, maxEntries int) *Store {
	s := &Store{ttl: ttl, capPerSh: maxEntries/numShards + 1}
	for i := range s.shards {
		s.shards[i].m = make(map[string]entry)
	}
	go s.janitor()
	return s
}

func (s *Store) pick(key string) *shard {
	h := 0
	for i := 0; i < len(key); i++ {
		h = h*31 + int(key[i])
	}
	return &s.shards[h&(numShards-1)]
}

func (s *Store) Get(key string) ([]byte, bool) {
	sh := s.pick(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.val, true
}

func (s *Store) Set(key string, val []byte) {
	sh := s.pick(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	// ponytail: over-cap shard evicts a random entry, not LRU. Upgrade to
	// a real LRU per shard if hot-key eviction shows up in production.
	if len(sh.m) >= s.capPerSh {
		for k := range sh.m {
			delete(sh.m, k)
			break
		}
	}
	sh.m[key] = entry{val: val, exp: time.Now().Add(s.ttl)}
}

func (s *Store) janitor() {
	for range time.Tick(30 * time.Second) {
		now := time.Now()
		for i := range s.shards {
			sh := &s.shards[i]
			sh.mu.Lock()
			for k, e := range sh.m {
				if now.After(e.exp) {
					delete(sh.m, k)
				}
			}
			sh.mu.Unlock()
		}
	}
}

var (
	dataStore  *Store
	tokenStore *Store
)

// --- helpers ---

func makeUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func validToken(r *http.Request, token string) bool {
	_, ok := tokenStore.Get("token:" + token)
	return ok
}

// --- handlers ---

func handleRegister(w http.ResponseWriter, r *http.Request) {
	server, client := makeUUID(), makeUUID()
	tokenStore.Set("token:"+server, []byte(strconv.FormatInt(time.Now().UnixMilli(), 10)))
	tokenStore.Set("token:"+client, []byte(strconv.FormatInt(time.Now().UnixMilli(), 10)))
	writeJSON(w, map[string]any{"server": server, "client": client, "expiration": int(expiration.Seconds())})
}

func handleWrite(w http.ResponseWriter, r *http.Request) {
	if !validToken(r, r.PathValue("token")) {
		http.Error(w, "Invalid Request Parameters", http.StatusUnauthorized)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDataSize+1))
	if err != nil || len(data) > maxDataSize {
		http.Error(w, "Data exceeds maximum size of "+strconv.Itoa(maxDataSize), http.StatusRequestEntityTooLarge)
		return
	}
	id := makeUUID()
	dataStore.Set("size:"+id, []byte(strconv.Itoa(len(data))))
	dataStore.Set("data:"+id, data)
	writeJSON(w, map[string]string{"id": id})
}

func handleRead(w http.ResponseWriter, r *http.Request) {
	if !validToken(r, r.PathValue("token")) {
		http.Error(w, "Invalid Request Parameters", http.StatusUnauthorized)
		return
	}
	data, ok := dataStore.Get("data:" + r.PathValue("id"))
	if !ok {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	// ServeContent handles Range requests (206), Content-Length, Content-Type.
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func handleSize(w http.ResponseWriter, r *http.Request) {
	if !validToken(r, r.PathValue("token")) {
		http.Error(w, "", http.StatusUnauthorized)
		return
	}
	size, ok := dataStore.Get("size:" + r.PathValue("id"))
	if !ok {
		http.Error(w, "Size not found", http.StatusNotFound)
		return
	}
	// Original returns size as a JSON string, keep compatible.
	writeJSON(w, map[string]string{"size": string(size)})
}

func handleRevision(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]int{"revision": 1})
}

func main() {
	dataStore = NewStore(expiration, maxEntries)
	tokenStore = NewStore(tokenExpiration, maxEntries)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/CFC-Servers/gm_express", http.StatusFound)
	})
	mux.HandleFunc("GET /v1/register", handleRegister)
	mux.HandleFunc("GET /v1/read/{token}/{id}", handleRead)
	mux.HandleFunc("GET /v1/size/{token}/{id}", handleSize)
	mux.HandleFunc("POST /v1/write/{token}", handleWrite)
	mux.HandleFunc("GET /v1/revision", handleRevision)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found - you may need to update the gm_express addon!", http.StatusNotAcceptable)
	})

	host := os.Getenv("API_HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "3000"
	}

	addr := host + ":" + port
	log.Printf("Express (go) listening on %s, data TTL %s, max entries %d", addr, expiration, maxEntries)
	log.Fatal((&http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}).ListenAndServe())
}
