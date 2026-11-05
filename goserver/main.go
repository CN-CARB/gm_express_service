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
	"time"
)

const (
	maxDataSize         = 24 * 1024 * 1024
	defaultMaxDataBytes = 512 * 1024 * 1024
)

var (
	expiration      = time.Duration(envInt("GM_EXPRESS_EXPIRATION", 24*60*60)) * time.Second // data TTL
	tokenExpiration = 24 * time.Hour
	maxEntries      = envInt("GM_EXPRESS_MAX_ENTRIES", 1024)
	maxDataBytes    = envInt("GM_EXPRESS_MAX_BYTES", defaultMaxDataBytes)
)

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

const tokenMaxEntries = 4096

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
	data, err := json.Marshal(v)
	if err != nil {
		writeText(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	_, _ = w.Write(data)
}

func writeText(w http.ResponseWriter, body string, status int) {
	w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func validToken(token string) bool {
	_, ok := tokenStore.Get(token)
	return ok
}

// --- handlers ---

func handleRegister(w http.ResponseWriter, r *http.Request) {
	server, client := makeUUID(), makeUUID()
	tokenStore.Set(server, nil)
	tokenStore.Set(client, nil)
	writeJSON(w, map[string]any{"server": server, "client": client, "expiration": int(tokenExpiration.Seconds())})
}

func handleWrite(w http.ResponseWriter, r *http.Request) {
	if !validToken(r.PathValue("token")) {
		writeText(w, "Invalid Request Parameters", http.StatusUnauthorized)
		return
	}
	if r.ContentLength > maxDataSize {
		writeText(w, "Data exceeds maximum size of "+strconv.Itoa(maxDataSize), http.StatusRequestEntityTooLarge)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDataSize+1))
	if err != nil || len(data) > maxDataSize {
		writeText(w, "Data exceeds maximum size of "+strconv.Itoa(maxDataSize), http.StatusRequestEntityTooLarge)
		return
	}
	id := makeUUID()
	if !dataStore.Set(id, data) {
		writeText(w, "Failed to store data", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func handleRead(w http.ResponseWriter, r *http.Request) {
	if !validToken(r.PathValue("token")) {
		writeText(w, "Invalid Request Parameters", http.StatusUnauthorized)
		return
	}
	data, ok := dataStore.Get(r.PathValue("id"))
	if !ok {
		writeText(w, "404 Not Found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func handleSize(w http.ResponseWriter, r *http.Request) {
	if !validToken(r.PathValue("token")) {
		writeText(w, "", http.StatusUnauthorized)
		return
	}
	data, ok := dataStore.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, struct{}{})
		return
	}
	// Original returns size as a JSON string, keep compatible.
	writeJSON(w, map[string]string{"size": strconv.Itoa(len(data))})
}

func handleRevision(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]int{"revision": 1})
}

func newMux(data, tokens *Store) http.Handler {
	dataStore, tokenStore = data, tokens
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/CN-CARB/NetLFS", http.StatusFound)
	})
	mux.HandleFunc("GET /v1/register", handleRegister)
	mux.HandleFunc("GET /v1/read/{token}/{id}", handleRead)
	mux.HandleFunc("GET /v1/size/{token}/{id}", handleSize)
	mux.HandleFunc("POST /v1/write/{token}", handleWrite)
	mux.HandleFunc("GET /v1/revision", handleRevision)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeText(w, "404 Not Found", http.StatusNotFound)
			return
		}
		writeText(w, "Not Found - you may need to update the gm_express addon!", http.StatusNotAcceptable)
	})
	return mux
}

func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func main() {
	dataStore = NewStore(expiration, maxEntries, maxDataBytes)
	tokenStore = NewStore(tokenExpiration, tokenMaxEntries, 0)

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
	log.Fatal(newServer(addr, newMux(dataStore, tokenStore)).ListenAndServe())
}
