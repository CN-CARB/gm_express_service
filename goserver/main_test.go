package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type registration struct {
	Server     string `json:"server"`
	Client     string `json:"client"`
	Expiration int    `json:"expiration"`
}

type observedReader struct {
	read bool
}

func (r *observedReader) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

func Test_HTTP_roundtrip_matches_legacy_service(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(5*time.Minute, 1024, 0))
	write := request(t, handler, http.MethodPost, "/v1/write/"+registration.Server, "0123456789", "")
	var written struct {
		ID string `json:"id"`
	}
	decodeJSON(t, write, &written)
	read := request(t, handler, http.MethodGet, "/v1/read/"+registration.Client+"/"+written.ID, "", "")
	size := request(t, handler, http.MethodGet, "/v1/size/"+registration.Client+"/"+written.ID, "", "")
	revision := request(t, handler, http.MethodGet, "/v1/revision", "", "")

	if registration.Server == "" || registration.Client == "" || registration.Expiration != 300 {
		t.Fatalf("unexpected registration: %+v", registration)
	}
	assertStatus(t, write, http.StatusOK)
	if written.ID == "" {
		t.Fatal("write response missing id")
	}
	assertResponse(t, read, http.StatusOK, "0123456789")
	assertResponse(t, size, http.StatusOK, `{"size":"10"}`)
	assertResponse(t, revision, http.StatusOK, `{"revision":1}`)
}

func Test_HTTP_errors_match_legacy_service(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(5*time.Minute, 1024, 0))

	tests := []struct {
		name   string
		method string
		path   string
		status int
		body   string
	}{
		{"read rejects invalid token", http.MethodGet, "/v1/read/bad/missing", http.StatusUnauthorized, "Invalid Request Parameters"},
		{"size rejects invalid token with empty body", http.MethodGet, "/v1/size/bad/missing", http.StatusUnauthorized, ""},
		{"missing data is not found", http.MethodGet, "/v1/read/" + registration.Server + "/missing", http.StatusNotFound, "404 Not Found"},
		{"missing size is empty json", http.MethodGet, "/v1/size/" + registration.Server + "/missing", http.StatusOK, "{}"},
		{"unknown get returns upgrade hint", http.MethodGet, "/unknown", http.StatusNotAcceptable, "Not Found - you may need to update the gm_express addon!"},
		{"unsupported method is not found", http.MethodPost, "/v1/revision", http.StatusNotFound, "404 Not Found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := request(t, handler, tt.method, tt.path, "", "")
			assertResponse(t, response, tt.status, tt.body)
		})
	}
}

func Test_HTTP_ranges_match_legacy_service(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(5*time.Minute, 1024, 0))
	write := request(t, handler, http.MethodPost, "/v1/write/"+registration.Server, "0123456789", "")
	var written struct {
		ID string `json:"id"`
	}
	decodeJSON(t, write, &written)
	path := "/v1/read/" + registration.Server + "/" + written.ID

	tests := []struct {
		name         string
		rangeHeader  string
		status       int
		body         string
		contentRange string
	}{
		{"explicit range", "bytes=0-3", http.StatusPartialContent, "0123", "bytes 0-2/10"},
		{"suffix range", "bytes=-3", http.StatusPartialContent, "0123", "bytes 0-2/10"},
		{"malformed range", "nonsense", http.StatusOK, "0123456789", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := request(t, handler, http.MethodGet, path, "", tt.rangeHeader)
			assertResponse(t, response, tt.status, tt.body)
			if got := response.Header().Get("Content-Range"); got != tt.contentRange {
				t.Fatalf("Content-Range = %q, want %q", got, tt.contentRange)
			}
		})
	}
}

func Test_HTTP_data_and_size_evict_together(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(5*time.Minute, 1, 0))
	firstWrite := request(t, handler, http.MethodPost, "/v1/write/"+registration.Server, "first", "")
	var first struct {
		ID string `json:"id"`
	}
	decodeJSON(t, firstWrite, &first)
	secondWrite := request(t, handler, http.MethodPost, "/v1/write/"+registration.Server, "second", "")
	var second struct {
		ID string `json:"id"`
	}
	decodeJSON(t, secondWrite, &second)

	firstRead := request(t, handler, http.MethodGet, "/v1/read/"+registration.Server+"/"+first.ID, "", "")
	firstSize := request(t, handler, http.MethodGet, "/v1/size/"+registration.Server+"/"+first.ID, "", "")
	secondRead := request(t, handler, http.MethodGet, "/v1/read/"+registration.Server+"/"+second.ID, "", "")
	secondSize := request(t, handler, http.MethodGet, "/v1/size/"+registration.Server+"/"+second.ID, "", "")

	assertResponse(t, firstRead, http.StatusNotFound, "404 Not Found")
	assertResponse(t, firstSize, http.StatusOK, "{}")
	assertResponse(t, secondRead, http.StatusOK, "second")
	assertResponse(t, secondSize, http.StatusOK, `{"size":"6"}`)
}

func Test_Store_Get_releases_expired_entry(t *testing.T) {
	store := NewStore(-time.Second, 1, 0)
	store.Set("expired", []byte("payload"))

	_, found := store.Get("expired")

	if found {
		t.Fatal("expired entry was found")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.m) != 0 {
		t.Fatalf("store retained %d expired entries", len(store.m))
	}
}

func Test_Store_Set_releases_expired_entries_at_capacity(t *testing.T) {
	store := NewStore(-time.Second, 1, 0)
	store.Set("expired", []byte("payload"))

	stored := store.Set("replacement", nil)

	if !stored || len(store.m) != 1 || store.usedBytes != 0 {
		t.Fatalf("unexpected store state: stored=%t entries=%d bytes=%d", stored, len(store.m), store.usedBytes)
	}
	if _, found := store.m["expired"]; found {
		t.Fatal("expired entry was retained")
	}
}

func Test_HTTP_rejects_known_oversized_body_before_reading(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(5*time.Minute, 1024, 0))
	body := &observedReader{}
	req := httptest.NewRequest(http.MethodPost, "/v1/write/"+registration.Server, body)
	req.ContentLength = maxDataSize + 1
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	assertResponse(t, response, http.StatusRequestEntityTooLarge, "Data exceeds maximum size of 25165824")
	if body.read {
		t.Fatal("oversized body was read")
	}
}

func Test_HTTP_rejects_write_when_memory_budget_is_full(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(5*time.Minute, 1024, 5))
	first := request(t, handler, http.MethodPost, "/v1/write/"+registration.Server, "12345", "")
	assertStatus(t, first, http.StatusOK)

	second := request(t, handler, http.MethodPost, "/v1/write/"+registration.Server, "6", "")

	assertResponse(t, second, http.StatusInternalServerError, "Failed to store data")
}

func Test_newServer_bounds_connection_lifetimes(t *testing.T) {
	server := newServer(":0", http.NotFoundHandler())

	if server.ReadTimeout < 5*time.Minute || server.WriteTimeout < 5*time.Minute || server.IdleTimeout == 0 {
		t.Fatalf("transfer timeouts too short: %+v", server)
	}
}

func registeredHandler(t *testing.T, data *Store) (http.Handler, registration) {
	t.Helper()
	handler := newMux(data, NewStore(24*time.Hour, 4096, 0))
	response := request(t, handler, http.MethodGet, "/v1/register", "", "")
	assertStatus(t, response, http.StatusOK)
	var got registration
	decodeJSON(t, response, &got)
	return handler, got
}

func request(t *testing.T, handler http.Handler, method, path, body, rangeHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, want, response.Body.String())
	}
}

func assertResponse(t *testing.T, response *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	assertStatus(t, response, status)
	if got := response.Body.String(); got != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
}
