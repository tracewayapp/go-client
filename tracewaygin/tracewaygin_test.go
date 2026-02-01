package tracewaygin

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gin-gonic/gin"
)

var testMiddleware gin.HandlerFunc

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	testMiddleware = New("testtoken@http://localhost:19876/noop")
	os.Exit(m.Run())
}

func serveGin(t *testing.T, mw gin.HandlerFunc, method, path string, body *bytes.Buffer, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.Use(mw)
	switch method {
	case "GET":
		r.GET(path, handler)
	case "POST":
		r.POST(path, handler)
	case "PUT":
		r.PUT(path, handler)
	case "DELETE":
		r.DELETE(path, handler)
	default:
		r.Handle(method, path, handler)
	}

	rec := httptest.NewRecorder()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	r.ServeHTTP(rec, req)
	return rec
}
func TestFormatErrorChain(t *testing.T) {
	err := fmt.Errorf("something went wrong")
	result := formatErrorChain(err)
	if !strings.Contains(result, "something went wrong") {
		t.Errorf("expected error message in output, got %q", result)
	}
	if !strings.Contains(result, "errors.errorString") && !strings.Contains(result, "fmt.wrapError") {
		if !strings.Contains(result, ":") {
			t.Errorf("expected 'type: message' format, got %q", result)
		}
	}
}

func TestExtractStackFromError(t *testing.T) {
	plainErr := fmt.Errorf("plain error")
	frames := extractStackFromError(plainErr)
	if frames != nil {
		t.Errorf("expected nil frames for plain error, got %d frames", len(frames))
	}

	stackErr := &traceway.StackTraceError{
		Err: fmt.Errorf("with stack"),
		Frames: []runtime.Frame{
			{Function: "test.Func", File: "test.go", Line: 42},
		},
	}
	frames = extractStackFromError(stackErr)
	if frames == nil {
		t.Fatal("expected non-nil frames for StackTraceError")
	}
	if len(frames) != 1 {
		t.Errorf("expected 1 frame, got %d", len(frames))
	}
}

func TestIsStaticRoute(t *testing.T) {
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)

	var isStatic bool
	r.Use(func(c *gin.Context) {
		c.Next()
		isStatic = isStaticRoute(c)
	})
	r.GET("/check", func(c *gin.Context) {})
	req2 := httptest.NewRequest("GET", "/check", nil)
	rec2 := httptest.NewRecorder()
	_ = rec
	_ = req
	r.ServeHTTP(rec2, req2)

	if isStatic {
		t.Error("expected normal route to not be detected as static")
	}
}
func TestWithRepanic(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithRepanic(true)(opts)
	if !opts.repanic {
		t.Error("expected repanic=true")
	}
	WithRepanic(false)(opts)
	if opts.repanic {
		t.Error("expected repanic=false")
	}
}

func TestWithRecordUnmatched(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithRecordUnmatched(true)(opts)
	if !opts.recordUnmatched {
		t.Error("expected recordUnmatched=true")
	}
}

func TestWithRecordStatic(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithRecordStatic(true)(opts)
	if !opts.recordStatic {
		t.Error("expected recordStatic=true")
	}
}

func TestWithIgnoredPaths(t *testing.T) {
	opts := &TracewayGinOptions{}
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
	opts := &TracewayGinOptions{}
	WithOnErrorRecording(RecordingUrl | RecordingBody)(opts)
	if opts.onErrorRecording != RecordingUrl|RecordingBody {
		t.Errorf("expected flags 0b0101, got %08b", opts.onErrorRecording)
	}
}

func TestWithDebug(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithDebug(true)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMaxCollectionFrames(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithMaxCollectionFrames(20)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithCollectionInterval(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithCollectionInterval(10 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithUploadTimeout(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithUploadTimeout(5 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMetricsInterval(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithMetricsInterval(60 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithVersion(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithVersion("1.0.0")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithServerName(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithServerName("my-server")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithSampleRate(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithSampleRate(0.5)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithErrorSampleRate(t *testing.T) {
	opts := &TracewayGinOptions{}
	WithErrorSampleRate(0.25)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}
func TestMiddleware_NormalRequest(t *testing.T) {
	rec := serveGin(t, testMiddleware, "GET", "/hello", nil, func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got %q", rec.Body.String())
	}
}

func TestMiddleware_CustomStatusCode(t *testing.T) {
	rec := serveGin(t, testMiddleware, "GET", "/notfound", nil, func(c *gin.Context) {
		c.String(http.StatusNotFound, "not found")
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestMiddleware_ContextHasAttributesAndTrace(t *testing.T) {
	rec := serveGin(t, testMiddleware, "GET", "/ctx", nil, func(c *gin.Context) {
		attributesVal, exists := c.Get(string(traceway.CtxAttributesKey))
		if !exists || attributesVal == nil {
			c.String(500, "attributes not in gin context")
			return
		}
		txnVal, exists := c.Get(string(traceway.CtxTraceKey))
		if !exists || txnVal == nil {
			c.String(500, "txn not in gin context")
			return
		}

		attributes := traceway.GetAttributesFromContext(c.Request.Context())
		txn := traceway.GetTraceFromContext(c.Request.Context())
		if attributes == nil {
			c.String(500, "attributes nil in request context")
			return
		}
		if txn == nil {
			c.String(500, "txn nil in request context")
			return
		}
		if txn.Id == "" {
			c.String(500, "txn id empty")
			return
		}
		c.String(200, "ok")
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiddleware_IgnoredPathSkipsRecording(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithIgnoredPaths("/health"))
	called := false
	rec := serveGin(t, mw, "GET", "/health", nil, func(c *gin.Context) {
		called = true
		c.String(200, "ok")
	})
	if !called {
		t.Error("handler should still be called for ignored paths")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_UnmatchedRoute_DefaultSkipped(t *testing.T) {
	r := gin.New()
	mw := New("testtoken@http://localhost:19876/noop", WithRecordUnmatched(false))
	r.Use(mw)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/unmatched", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unmatched route, got %d", rec.Code)
	}
}

func TestMiddleware_UnmatchedRoute_Recorded(t *testing.T) {
	r := gin.New()
	mw := New("testtoken@http://localhost:19876/noop", WithRecordUnmatched(true))
	r.Use(mw)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/unmatched-recorded", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestMiddleware_PanicRecovery_NoRepanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(false))
	rec := serveGin(t, mw, "GET", "/panic", nil, func(c *gin.Context) {
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
	serveGin(t, mw, "GET", "/panic", nil, func(c *gin.Context) {
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
			t.Errorf("expected original error preserved, got %v", err)
		}
	}()
	serveGin(t, mw, "GET", "/panic-err", nil, func(c *gin.Context) {
		panic(origErr)
	})
	t.Fatal("should not reach here")
}

func TestMiddleware_GinErrors(t *testing.T) {
	rec := serveGin(t, testMiddleware, "GET", "/ginerr", nil, func(c *gin.Context) {
		c.Error(fmt.Errorf("some gin error"))
		c.String(200, "ok")
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_GinErrors_WithStackTraceError(t *testing.T) {
	rec := serveGin(t, testMiddleware, "GET", "/ginerr-stack", nil, func(c *gin.Context) {
		stackErr := &traceway.StackTraceError{
			Err: fmt.Errorf("wrapped error"),
			Frames: []runtime.Frame{
				{Function: "pkg.Func", File: "file.go", Line: 10},
			},
		}
		c.Error(stackErr)
		c.String(200, "ok")
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_AttributesTagsPropagated(t *testing.T) {
	rec := serveGin(t, testMiddleware, "GET", "/tags", nil, func(c *gin.Context) {
		attributes := traceway.GetAttributesFromContext(c)
		attributes.SetTag("user_id", "42")
		c.String(200, "ok")
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
	rec := serveGin(t, mw, "GET", "/error-url", nil, func(c *gin.Context) {
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
	r := gin.New()
	r.Use(mw)
	r.GET("/error-query", func(c *gin.Context) {
		panic("query test")
	})
	req := httptest.NewRequest("GET", "/error-query?foo=bar", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
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
	r := gin.New()
	r.Use(mw)
	r.POST("/error-body", func(c *gin.Context) {
		panic("body test")
	})
	req := httptest.NewRequest("POST", "/error-body", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_OnErrorRecording_Headers(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingHeader),
	)
	rec := serveGin(t, mw, "GET", "/error-headers", nil, func(c *gin.Context) {
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
	r := gin.New()
	r.Use(mw)
	r.POST("/error-body-text", func(c *gin.Context) {
		panic("body non-json test")
	})
	body := bytes.NewBufferString("plain text body")
	req := httptest.NewRequest("POST", "/error-body-text", body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_MultipleRequestsSequential(t *testing.T) {
	r := gin.New()
	r.Use(testMiddleware)
	r.GET("/seq", func(c *gin.Context) {
		txn := traceway.GetTraceFromContext(c.Request.Context())
		if txn == nil {
			c.String(500, "no txn")
			return
		}
		c.String(200, "ok")
	})

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/seq", nil)
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d; body=%s", i, rec.Code, rec.Body.String())
		}
	}
}
func TestWrapAndExecute_NoPanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(false))
	var handlerCalled bool
	rec := serveGin(t, mw, "GET", "/wae-ok", nil, func(c *gin.Context) {
		handlerCalled = true
		c.String(200, "ok")
	})
	if !handlerCalled {
		t.Error("handler should have been called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestWrapAndExecute_PanicString_Repanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("expected error type on repanic, got %T: %v", r, r)
		}
		if !strings.Contains(err.Error(), "test panic") {
			t.Errorf("expected error to contain panic value, got %v", err)
		}
	}()
	serveGin(t, mw, "GET", "/wae-str", nil, func(c *gin.Context) {
		panic("test panic")
	})
	t.Fatal("should not reach here")
}

func TestWrapAndExecute_PanicError_Repanic(t *testing.T) {
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
			t.Errorf("expected original error preserved, got %v", err)
		}
	}()
	serveGin(t, mw, "GET", "/wae-err", nil, func(c *gin.Context) {
		panic(origErr)
	})
	t.Fatal("should not reach here")
}

func TestWrapAndExecute_PanicString_NoRepanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(false))
	rec := serveGin(t, mw, "GET", "/wae-norep", nil, func(c *gin.Context) {
		panic("test panic")
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

var _ = errors.New
