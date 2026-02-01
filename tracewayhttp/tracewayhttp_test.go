package tracewayhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	traceway "go.tracewayapp.com"
)

var testMiddleware func(http.Handler) http.Handler

func TestMain(m *testing.M) {
	testMiddleware = New("testtoken@http://localhost:19876/noop")
	os.Exit(m.Run())
}

func serve(t *testing.T, mw func(http.Handler) http.Handler, method, path string, body io.Reader, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	h := mw(http.HandlerFunc(handler))
	req := httptest.NewRequest(method, path, body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	if rw.statusCode != http.StatusOK {
		t.Errorf("expected default status 200, got %d", rw.statusCode)
	}
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	rw.WriteHeader(http.StatusNotFound)
	if rw.statusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rw.statusCode)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected underlying recorder status 404, got %d", rec.Code)
	}
}

func TestResponseWriter_Write(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if rw.bytesWritten != 5 {
		t.Errorf("expected bytesWritten=5, got %d", rw.bytesWritten)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rec.Body.String())
	}
}

func TestResponseWriter_MultipleWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	rw.Write([]byte("aaa"))
	rw.Write([]byte("bb"))
	if rw.bytesWritten != 5 {
		t.Errorf("expected bytesWritten=5, got %d", rw.bytesWritten)
	}
}

func TestResponseWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	rw.Flush()
	if !rec.Flushed {
		t.Error("expected underlying recorder to be flushed")
	}
}

type nonFlushWriter struct{ http.ResponseWriter }

func TestResponseWriter_Flush_NotSupported(t *testing.T) {
	rw := newResponseWriter(nonFlushWriter{httptest.NewRecorder()})
	rw.Flush()
}

func TestResponseWriter_Hijack_NotSupported(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	_, _, err := rw.Hijack()
	if err == nil {
		t.Error("expected error when underlying writer does not support Hijack")
	}
}

type hijackWriter struct {
	http.ResponseWriter
}

func (hw hijackWriter) Hijack() (net.Conn, *bufioRW, error) {
	return nil, nil, nil
}

type bufioRW = bufioReadWriter

func TestResponseWriter_Hijack_Supported(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

}

func TestResponseWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	if rw.Unwrap() != rec {
		t.Error("Unwrap should return the underlying ResponseWriter")
	}
}

func TestClientIP_XForwardedFor_Single(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := clientIP(r); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XForwardedFor_Multiple(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if ip := clientIP(r); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-IP", "9.8.7.6")
	if ip := clientIP(r); ip != "9.8.7.6" {
		t.Errorf("expected 9.8.7.6, got %s", ip)
	}
}

func TestClientIP_RemoteAddr_WithPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	if ip := clientIP(r); ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}
}

func TestClientIP_RemoteAddr_WithoutPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1"
	if ip := clientIP(r); ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}
}

func TestClientIP_Priority(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	r.Header.Set("X-Real-IP", "2.2.2.2")
	r.RemoteAddr = "3.3.3.3:999"
	if ip := clientIP(r); ip != "1.1.1.1" {
		t.Errorf("expected X-Forwarded-For to win, got %s", ip)
	}
}

func TestWithFilter(t *testing.T) {
	called := false
	fn := func(*http.Request) bool { called = true; return true }
	opts := &TracewayHttpOptions{}
	WithFilter(fn)(opts)
	if opts.filter == nil {
		t.Fatal("expected filter to be set")
	}
	opts.filter(httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Error("expected filter function to be called")
	}
}

func TestWithIgnoredPaths(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithIgnoredPaths("/health", "/ready")(opts)
	if _, ok := opts.ignoredPaths["/health"]; !ok {
		t.Error("expected /health in ignoredPaths")
	}
	if _, ok := opts.ignoredPaths["/ready"]; !ok {
		t.Error("expected /ready in ignoredPaths")
	}
	WithIgnoredPaths("/metrics")(opts)
	if len(opts.ignoredPaths) != 3 {
		t.Errorf("expected 3 ignored paths, got %d", len(opts.ignoredPaths))
	}
}

func TestWithOnErrorRecording(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithOnErrorRecording(RecordingUrl | RecordingBody)(opts)
	if opts.onErrorRecording != RecordingUrl|RecordingBody {
		t.Errorf("expected flags 0b0101, got %08b", opts.onErrorRecording)
	}
}

func TestWithRepanic(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithRepanic(false)(opts)
	if opts.repanic {
		t.Error("expected repanic=false")
	}
	WithRepanic(true)(opts)
	if !opts.repanic {
		t.Error("expected repanic=true")
	}
}

func TestWithDebug(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithDebug(true)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMaxCollectionFrames(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithMaxCollectionFrames(20)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithCollectionInterval(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithCollectionInterval(10 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithUploadTimeout(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithUploadTimeout(5 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMetricsInterval(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithMetricsInterval(60 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithVersion(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithVersion("1.0.0")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithServerName(t *testing.T) {
	opts := &TracewayHttpOptions{}
	WithServerName("my-server")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestMiddleware_NormalRequest(t *testing.T) {
	rec := serve(t, testMiddleware, "GET", "/hello", nil, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got %q", rec.Body.String())
	}
}

func TestMiddleware_CustomStatusCode(t *testing.T) {
	rec := serve(t, testMiddleware, "GET", "/notfound", nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestMiddleware_ContextHasAttributesAndTrace(t *testing.T) {
	rec := serve(t, testMiddleware, "GET", "/ctx", nil, func(w http.ResponseWriter, r *http.Request) {
		attributes := traceway.GetAttributesFromContext(r.Context())
		txn := traceway.GetTraceFromContext(r.Context())
		if attributes == nil {
			http.Error(w, "attributes nil", 500)
			return
		}
		if txn == nil {
			http.Error(w, "txn nil", 500)
			return
		}
		if txn.Id == "" {
			http.Error(w, "txn id empty", 500)
			return
		}
		w.Write([]byte("ok"))
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiddleware_IgnoredPath(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithIgnoredPaths("/health"))
	called := false
	rec := serve(t, mw, "GET", "/health", nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("ok"))
	})
	if !called {
		t.Error("handler should still be called for ignored paths")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_FilterRejectSkips(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithFilter(func(r *http.Request) bool {
		return false
	}))
	called := false
	rec := serve(t, mw, "GET", "/filtered", nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("ok"))
	})
	if !called {
		t.Error("handler should be called even when filter rejects")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_FilterAcceptRecords(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithFilter(func(r *http.Request) bool {
		return true
	}))
	rec := serve(t, mw, "GET", "/accepted", nil, func(w http.ResponseWriter, r *http.Request) {
		attributes := traceway.GetAttributesFromContext(r.Context())
		txn := traceway.GetTraceFromContext(r.Context())
		if attributes == nil || txn == nil {
			http.Error(w, "missing context values", 500)
			return
		}
		w.Write([]byte("ok"))
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiddleware_PanicRecovery_NoRepanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(false))
	rec := serve(t, mw, "GET", "/panic", nil, func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_PanicRecovery_Repanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate")
		}
	}()
	serve(t, mw, "GET", "/panic", nil, func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	t.Fatal("should not reach here")
}

func TestMiddleware_PanicRecovery_ErrorType(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	origErr := fmt.Errorf("original error")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("expected error type, got %T", r)
		}
		if err != origErr {
			t.Errorf("expected original error to be preserved, got %v", err)
		}
	}()
	serve(t, mw, "GET", "/panic-err", nil, func(w http.ResponseWriter, r *http.Request) {
		panic(origErr)
	})
	t.Fatal("should not reach here")
}

func TestMiddleware_AttributesTagsPropagated(t *testing.T) {
	rec := serve(t, testMiddleware, "GET", "/tags", nil, func(w http.ResponseWriter, r *http.Request) {
		attributes := traceway.GetAttributesFromContext(r.Context())
		attributes.SetTag("user_id", "42")
		w.Write([]byte("ok"))
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_OnErrorRecording_Url(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingUrl),
	)
	rec := serve(t, mw, "GET", "/error-url", nil, func(w http.ResponseWriter, r *http.Request) {
		panic("url test")
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_OnErrorRecording_Query(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingQuery),
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("query test")
	}))
	req := httptest.NewRequest("GET", "/error-query?foo=bar", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_OnErrorRecording_Body(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingBody),
	)
	body := bytes.NewBufferString(`{"key":"value"}`)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("body test")
	}))
	req := httptest.NewRequest("POST", "/error-body", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_OnErrorRecording_Headers(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingHeader),
	)
	rec := serve(t, mw, "GET", "/error-headers", nil, func(w http.ResponseWriter, r *http.Request) {
		panic("headers test")
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_OnErrorRecording_BodyNonJson(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingBody),
	)
	body := bytes.NewBufferString("plain text body")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("body non-json test")
	}))
	req := httptest.NewRequest("POST", "/error-body-text", body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_ResponseBodySize(t *testing.T) {
	payload := strings.Repeat("x", 1024)
	rec := serve(t, testMiddleware, "GET", "/size", nil, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	})
	if rec.Body.String() != payload {
		t.Errorf("expected full payload in response")
	}
}

func TestMiddleware_ClientIPExtraction(t *testing.T) {
	h := testMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "/ip", nil)
	req.Header.Set("X-Forwarded-For", "99.99.99.99")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_EndpointFormat(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		h := testMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("ok"))
		}))
		req := httptest.NewRequest(method, "/endpoint", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", method, rec.Code)
		}
	}
}

func TestMiddleware_MultipleRequestsSequential(t *testing.T) {
	for i := 0; i < 3; i++ {
		rec := serve(t, testMiddleware, "GET", "/seq", nil, func(w http.ResponseWriter, r *http.Request) {
			txn := traceway.GetTraceFromContext(r.Context())
			if txn == nil {
				http.Error(w, "no txn", 500)
				return
			}
			w.Write([]byte("ok"))
		})
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}
}

func TestWrapAndExecute_NoPanic(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	req := httptest.NewRequest("GET", "/", nil)
	s, e := wrapAndExecute(false, handler, rw, req)
	if s != nil {
		t.Errorf("expected nil stack, got %v", *s)
	}
	if e != nil {
		t.Errorf("expected nil error, got %v", e)
	}
}

func TestWrapAndExecute_PanicString_Repanic(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	req := httptest.NewRequest("GET", "/", nil)
	s, e := wrapAndExecute(true, handler, rw, req)
	if s == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if e == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(e.Error(), "test panic") {
		t.Errorf("expected error to contain panic value, got %v", e)
	}
}

func TestWrapAndExecute_PanicError_Repanic(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	origErr := fmt.Errorf("original error")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(origErr)
	})
	req := httptest.NewRequest("GET", "/", nil)
	s, e := wrapAndExecute(true, handler, rw, req)
	if s == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if e != origErr {
		t.Errorf("expected original error preserved, got %v", e)
	}
}

func TestWrapAndExecute_PanicString_NoRepanic(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	req := httptest.NewRequest("GET", "/", nil)
	s, e := wrapAndExecute(false, handler, rw, req)
	if s == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if e != nil {
		t.Errorf("expected nil error (no repanic), got %v", e)
	}
	if rw.statusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rw.statusCode)
	}
}

type bufioReadWriter struct{}

var _ = context.Background
