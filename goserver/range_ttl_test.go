package main

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test_HTTP_multipart_ranges(t *testing.T) {
	// Current Go releases default to one range for modules targeting Go 1.22.
	// Exercise multipart support without changing the service's default policy.
	t.Setenv("GODEBUG", "httpservecontentmaxranges=200")
	handler, registration := registeredHandler(t, NewStore(time.Hour, 10, 0))
	dataStore.Set("file", []byte(strings.Repeat("0123456789", 128)))
	response := request(t, handler, http.MethodGet, "/v1/read/"+registration.Client+"/file", "", "bytes=0-1,1278-1279")
	assertStatus(t, response, http.StatusPartialContent)
	mediaType, params, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
	if err != nil || mediaType != "multipart/byteranges" {
		t.Fatalf("unexpected multipart content type: %q, %v", mediaType, err)
	}
	if response.Header().Get("Content-Range") != "" {
		t.Fatal("multipart response must use per-part Content-Range headers")
	}
	if got := response.Header().Get("Content-Length"); got != strconv.Itoa(response.Body.Len()) {
		t.Fatalf("multipart Content-Length = %q, body size = %d", got, response.Body.Len())
	}
	reader := multipart.NewReader(response.Body, params["boundary"])
	for _, want := range []struct{ body, contentRange string }{
		{"01", "bytes 0-1/1280"},
		{"89", "bytes 1278-1279/1280"},
	} {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != want.body || part.Header.Get("Content-Range") != want.contentRange {
			t.Fatalf("unexpected part: body=%q range=%q", body, part.Header.Get("Content-Range"))
		}
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Fatalf("expected end of multipart response, got %v", err)
	}
}

func Test_HTTP_empty_file_and_head(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(time.Hour, 10, 0))
	dataStore.Set("empty", nil)
	dataStore.Set("file", []byte("0123456789"))
	path := "/v1/read/" + registration.Client + "/"
	for _, header := range []string{"", "bytes=0-0"} {
		// ServeContent ignores a non-overlapping range for an empty representation.
		response := request(t, handler, http.MethodGet, path+"empty", "", header)
		assertResponse(t, response, http.StatusOK, "")
		if response.Header().Get("Content-Length") != "0" || response.Header().Get("Content-Range") != "" {
			t.Fatalf("unexpected empty-file headers: %v", response.Header())
		}
	}
	response := request(t, handler, http.MethodHead, path+"file", "", "")
	assertResponse(t, response, http.StatusOK, "")
	if response.Header().Get("Content-Length") != "10" {
		t.Fatalf("HEAD must report full content length: %v", response.Header())
	}
	assertResponse(t, request(t, handler, http.MethodGet, "/v1/size/"+registration.Client+"/empty", "", ""), http.StatusOK, `{"size":"0"}`)
}

func Test_HTTP_ranges_still_require_token_and_file(t *testing.T) {
	handler, registration := registeredHandler(t, NewStore(time.Hour, 10, 0))
	dataStore.Set("file", []byte("0123456789"))
	assertStatus(t, request(t, handler, http.MethodGet, "/v1/read/bad/file", "", "bytes=0-3"), http.StatusUnauthorized)
	assertStatus(t, request(t, handler, http.MethodGet, "/v1/read/"+registration.Client+"/missing", "", "bytes=0-3"), http.StatusNotFound)
}

func Test_configured_data_and_token_lifetimes(t *testing.T) {
	// Verify the actual startup value, including the default when no override is set.
	wantDataTTL := time.Duration(envInt("GM_EXPRESS_EXPIRATION", 86400)) * time.Second
	if expiration != wantDataTTL || tokenExpiration != 24*time.Hour {
		t.Fatalf("unexpected lifetimes: data=%v (want %v) tokens=%v", expiration, wantDataTTL, tokenExpiration)
	}
}

func Test_registration_reports_token_lifetime_with_custom_data_TTL(t *testing.T) {
	original := expiration
	expiration = time.Hour
	t.Cleanup(func() { expiration = original })
	_, registration := registeredHandler(t, NewStore(expiration, 10, 0))
	if registration.Expiration != 86400 || tokenStore.ttl != 24*time.Hour {
		t.Fatalf("registration must report 24h token lifetime: %+v, TTL=%v", registration, tokenStore.ttl)
	}
	if dataStore.ttl != time.Hour {
		t.Fatal("data TTL override was lost")
	}
	// No wall-clock wait: check the expiry timestamps written for both credentials.
	for _, token := range []string{registration.Server, registration.Client} {
		tokenStore.mu.RLock()
		entry, ok := tokenStore.m[token]
		tokenStore.mu.RUnlock()
		remaining := time.Until(entry.exp)
		if !ok || remaining > 24*time.Hour || remaining < 24*time.Hour-time.Minute {
			t.Fatalf("unexpected stored token expiry: found=%v remaining=%v", ok, remaining)
		}
	}
}
