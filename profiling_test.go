package traceway

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func assertCompressedPprof(t *testing.T, b []byte) {
	t.Helper()
	if len(b) == 0 {
		t.Fatal("profile is empty")
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("profile is not a valid gzip stream (pprof must be gzip-compressed): %v", err)
	}
	defer r.Close()
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("profile gzip stream failed to fully inflate (truncated/corrupt?): %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("profile decompressed to zero bytes")
	}
}

type recordedRequest struct {
	method          string
	path            string
	authorization   string
	contentType     string
	contentEncoding string
	query           url.Values
	body            []byte
}

func TestProfileIngestURL(t *testing.T) {
	cases := []struct {
		name      string
		reportURL string
		want      string
		wantErr   bool
	}{
		{"report path", "http://localhost:19876/api/report", "http://localhost:19876/api/profiles/ingest", false},
		{"https host", "https://ingest.tracewayapp.com/api/report", "https://ingest.tracewayapp.com/api/profiles/ingest", false},
		{"unrelated path and port", "http://127.0.0.1:8080/noop", "http://127.0.0.1:8080/api/profiles/ingest", false},
		{"empty", "", "", true},
		{"no scheme or host", "/api/report", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := profileIngestURL(c.reportURL)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", c.reportURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.reportURL, err)
			}
			if got != c.want {
				t.Fatalf("profileIngestURL(%q) = %q, want %q", c.reportURL, got, c.want)
			}
		})
	}
}

func TestCaptureHeapProfile(t *testing.T) {
	b, err := captureHeapProfile()
	if err != nil {
		t.Fatalf("captureHeapProfile error: %v", err)
	}
	assertCompressedPprof(t, b)
}

func TestCaptureCPUProfile(t *testing.T) {
	b, err := captureCPUProfile(80 * time.Millisecond)
	if err != nil {
		t.Fatalf("captureCPUProfile error: %v", err)
	}
	assertCompressedPprof(t, b)

	if _, err := captureCPUProfile(80 * time.Millisecond); err != nil {
		t.Fatalf("second captureCPUProfile error (global state not reset?): %v", err)
	}
}

func newRecordingServer(t *testing.T, status *int, sink func(recordedRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sink(recordedRequest{
			method:          r.Method,
			path:            r.URL.Path,
			authorization:   r.Header.Get("Authorization"),
			contentType:     r.Header.Get("Content-Type"),
			contentEncoding: r.Header.Get("Content-Encoding"),
			query:           r.URL.Query(),
			body:            body,
		})
		code := http.StatusOK
		if status != nil {
			code = *status
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("{}"))
	}))
}

func TestUploadProfile(t *testing.T) {
	var (
		mu     sync.Mutex
		got    recordedRequest
		status = http.StatusOK
	)
	srv := newRecordingServer(t, &status, func(r recordedRequest) {
		mu.Lock()
		got = r
		mu.Unlock()
	})
	defer srv.Close()

	p := &profiler{
		url:        srv.URL + profileIngestPath,
		token:      "testtoken",
		service:    "checkout-api",
		serverName: "host-1",
		appVersion: "1.2.3",
		client:     srv.Client(),
	}

	payload := []byte{0x1f, 0x8b, 0x08, 0x00, 0x01, 0x02, 0x03}
	if err := p.uploadProfile(payload); err != nil {
		t.Fatalf("uploadProfile returned error on 200: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != profileIngestPath {
		t.Errorf("path = %q, want %q", got.path, profileIngestPath)
	}
	if got.authorization != "Bearer testtoken" {
		t.Errorf("Authorization = %q, want %q", got.authorization, "Bearer testtoken")
	}
	if v := got.query.Get("service"); v != "checkout-api" {
		t.Errorf("query service = %q, want checkout-api", v)
	}
	if v := got.query.Get("serverName"); v != "host-1" {
		t.Errorf("query serverName = %q, want host-1", v)
	}
	if v := got.query.Get("appVersion"); v != "1.2.3" {
		t.Errorf("query appVersion = %q, want 1.2.3", v)
	}
	if !bytes.Equal(got.body, payload) {
		t.Errorf("body = %x, want %x (must be raw pprof, no extra gzip)", got.body, payload)
	}
	if got.contentEncoding == "gzip" {
		t.Error("Content-Encoding must not be gzip; pprof bytes are already compressed")
	}
	if got.contentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got.contentType)
	}
}

func TestUploadProfileNon200ReturnsError(t *testing.T) {
	status := http.StatusInternalServerError
	srv := newRecordingServer(t, &status, func(recordedRequest) {})
	defer srv.Close()

	p := &profiler{
		url:        srv.URL + profileIngestPath,
		token:      "testtoken",
		service:    "svc",
		serverName: "host",
		appVersion: "v",
		client:     srv.Client(),
	}
	if err := p.uploadProfile([]byte{0x1f, 0x8b, 0x00}); err == nil {
		t.Fatal("expected error on non-200 response, got nil")
	}
}

func TestProfilingOptions(t *testing.T) {
	def := NewTracewayOptions()
	if def.profilingEnabled {
		t.Error("profiling should be disabled by default")
	}
	if def.profilingInterval != 60*time.Second {
		t.Errorf("default profilingInterval = %v, want 60s", def.profilingInterval)
	}

	enabled := NewTracewayOptions(WithProfiling("checkout-api"))
	if !enabled.profilingEnabled {
		t.Error("WithProfiling should enable profiling")
	}
	if enabled.profilingService != "checkout-api" {
		t.Errorf("profilingService = %q, want checkout-api", enabled.profilingService)
	}

	defaulted := NewTracewayOptions(WithProfiling(""))
	wantService := filepath.Base(os.Args[0])
	if defaulted.profilingService != wantService {
		t.Errorf("empty service should default to %q, got %q", wantService, defaulted.profilingService)
	}

	custom := NewTracewayOptions(WithProfiling("svc"), WithProfilingInterval(15*time.Second))
	if custom.profilingInterval != 15*time.Second {
		t.Errorf("profilingInterval = %v, want 15s", custom.profilingInterval)
	}
}

func TestCollectOnceSendsCPUAndHeap(t *testing.T) {
	var (
		mu   sync.Mutex
		reqs []recordedRequest
	)
	srv := newRecordingServer(t, nil, func(r recordedRequest) {
		mu.Lock()
		reqs = append(reqs, r)
		mu.Unlock()
	})
	defer srv.Close()

	p := &profiler{
		url:        srv.URL + profileIngestPath,
		token:      "testtoken",
		service:    "svc",
		serverName: "host",
		appVersion: "1.0.0",
		cpuWindow:  80 * time.Millisecond,
		client:     srv.Client(),
	}

	p.collectOnce()

	mu.Lock()
	defer mu.Unlock()

	if len(reqs) != 2 {
		t.Fatalf("collectOnce sent %d requests, want 2 (cpu + heap)", len(reqs))
	}
	for i, r := range reqs {
		if r.method != http.MethodPost {
			t.Errorf("req %d method = %q, want POST", i, r.method)
		}
		if r.path != profileIngestPath {
			t.Errorf("req %d path = %q, want %q", i, r.path, profileIngestPath)
		}
		if r.authorization != "Bearer testtoken" {
			t.Errorf("req %d Authorization = %q, want Bearer testtoken", i, r.authorization)
		}
		if r.query.Get("service") != "svc" || r.query.Get("serverName") != "host" || r.query.Get("appVersion") != "1.0.0" {
			t.Errorf("req %d query = %v, want service=svc serverName=host appVersion=1.0.0", i, r.query)
		}
		assertCompressedPprof(t, r.body)
	}
}
