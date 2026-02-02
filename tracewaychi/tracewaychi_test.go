package tracewaychi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	traceway "go.tracewayapp.com"
)

var testMiddleware func(http.Handler) http.Handler

func TestMain(m *testing.M) {
	testMiddleware = New("testtoken@http://localhost:19876/noop")
	os.Exit(m.Run())
}

func TestNew_ReturnsMiddleware(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop")
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}
}

func TestMiddleware_NormalRequest(t *testing.T) {
	h := testMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))
	req := httptest.NewRequest("GET", "/hello", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got %q", rec.Body.String())
	}
}

func TestMiddleware_ContextPropagation(t *testing.T) {
	h := testMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attr := traceway.GetAttributesFromContext(r.Context())
		tc := traceway.GetTraceFromContext(r.Context())
		if attr == nil {
			http.Error(w, "attributes nil", 500)
			return
		}
		if tc == nil || tc.Id == "" {
			http.Error(w, "trace nil or empty", 500)
			return
		}
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "/ctx", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRecordingFlags(t *testing.T) {
	if RecordingUrl != 1 {
		t.Errorf("expected RecordingUrl=1, got %d", RecordingUrl)
	}
	if RecordingQuery != 2 {
		t.Errorf("expected RecordingQuery=2, got %d", RecordingQuery)
	}
	if RecordingBody != 4 {
		t.Errorf("expected RecordingBody=4, got %d", RecordingBody)
	}
	if RecordingHeader != 8 {
		t.Errorf("expected RecordingHeader=8, got %d", RecordingHeader)
	}
}

func TestWithOptions(t *testing.T) {
	// Verify all option functions are callable
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithIgnoredPaths("/health"),
		WithOnErrorRecording(RecordingUrl|RecordingBody),
		WithFilter(func(r *http.Request) bool { return true }),
	)
	if mw == nil {
		t.Fatal("expected non-nil middleware with options")
	}
}
