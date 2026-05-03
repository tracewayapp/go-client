package tracewayfiber

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gofiber/fiber/v3"
)

var testMiddleware fiber.Handler
var defaultTestConfig = fiber.TestConfig{
	Timeout: -1,
}

func TestMain(m *testing.M) {
	testMiddleware = New("testtoken@http://localhost:19876/noop")
	os.Exit(m.Run())
}

func serveFiber(t *testing.T, mw fiber.Handler, method, path string, body []byte, handler fiber.Handler) *http.Response {
	t.Helper()
	app := fiber.New()
	app.Use(mw)

	switch method {
	case "GET":
		app.Get(path, handler)
	case "POST":
		app.Post(path, handler)
	case "PUT":
		app.Put(path, handler)
	case "DELETE":
		app.Delete(path, handler)
	default:
		app.Add([]string{method}, path, handler)
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reqBody)

	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
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

func TestWithRepanic(t *testing.T) {
	opts := &TracewayFiberOptions{}
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
	opts := &TracewayFiberOptions{}
	WithRecordUnmatched(true)(opts)
	if !opts.recordUnmatched {
		t.Error("expected recordUnmatched=true")
	}
}

func TestWithFilter(t *testing.T) {
	called := false
	fn := func(fiber.Ctx) bool { called = true; return true }
	opts := &TracewayFiberOptions{}
	WithFilter(fn)(opts)
	if opts.filter == nil {
		t.Fatal("expected filter to be set")
	}
	// We can't easily construct a fiber.Ctx outside of Fiber, so just check the function is set.
	_ = called
}

func TestWithIgnoredPaths(t *testing.T) {
	opts := &TracewayFiberOptions{}
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
	opts := &TracewayFiberOptions{}
	WithOnErrorRecording(RecordingUrl | RecordingBody)(opts)
	if opts.onErrorRecording != RecordingUrl|RecordingBody {
		t.Errorf("expected flags 0b0101, got %08b", opts.onErrorRecording)
	}
}

func TestWithDebug(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithDebug(true)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMaxCollectionFrames(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithMaxCollectionFrames(20)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithCollectionInterval(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithCollectionInterval(10 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithUploadTimeout(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithUploadTimeout(5 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMetricsInterval(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithMetricsInterval(60 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithVersion(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithVersion("1.0.0")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithServerName(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithServerName("my-server")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithSampleRate(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithSampleRate(0.5)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithErrorSampleRate(t *testing.T) {
	opts := &TracewayFiberOptions{}
	WithErrorSampleRate(0.25)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestMiddleware_NormalRequest(t *testing.T) {
	resp := serveFiber(t, testMiddleware, "GET", "/hello", nil, func(c fiber.Ctx) error {
		return c.SendString("OK")
	})
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "OK" {
		t.Errorf("expected body 'OK', got %q", string(body))
	}
}

func TestMiddleware_CustomStatusCode(t *testing.T) {
	resp := serveFiber(t, testMiddleware, "GET", "/notfound", nil, func(c fiber.Ctx) error {
		return c.Status(404).SendString("not found")
	})
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMiddleware_ContextHasAttributesAndTrace(t *testing.T) {
	resp := serveFiber(t, testMiddleware, "GET", "/ctx", nil, func(c fiber.Ctx) error {
		attributes := GetAttributesFromCtx(c)
		tc := GetTraceFromCtx(c)
		if attributes == nil {
			return c.Status(500).SendString("attributes nil")
		}
		if tc == nil {
			return c.Status(500).SendString("trace nil")
		}
		if tc.Id == "" {
			return c.Status(500).SendString("trace id empty")
		}
		return c.SendString("ok")
	})
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Errorf("expected 200, got %d; body=%s", resp.StatusCode, string(body))
	}
}

func TestMiddleware_IgnoredPathSkipsRecording(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithIgnoredPaths("/health"))
	called := false
	resp := serveFiber(t, mw, "GET", "/health", nil, func(c fiber.Ctx) error {
		called = true
		return c.SendString("ok")
	})
	if !called {
		t.Error("handler should still be called for ignored paths")
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMiddleware_FilterRejectSkips(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithFilter(func(c fiber.Ctx) bool {
		return false
	}))
	called := false
	resp := serveFiber(t, mw, "GET", "/filtered", nil, func(c fiber.Ctx) error {
		called = true
		return c.SendString("ok")
	})
	if !called {
		t.Error("handler should be called even when filter rejects")
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMiddleware_FilterAcceptRecords(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithFilter(func(c fiber.Ctx) bool {
		return true
	}))
	resp := serveFiber(t, mw, "GET", "/accepted", nil, func(c fiber.Ctx) error {
		attributes := GetAttributesFromCtx(c)
		tc := GetTraceFromCtx(c)
		if attributes == nil || tc == nil {
			return c.Status(500).SendString("missing context values")
		}
		return c.SendString("ok")
	})
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Errorf("expected 200, got %d; body=%s", resp.StatusCode, string(body))
	}
}

func TestMiddleware_PanicRecovery_NoRepanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(false))
	resp := serveFiber(t, mw, "GET", "/panic", nil, func(c fiber.Ctx) error {
		panic("boom")
	})
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_PanicRecovery_Repanic(t *testing.T) {
	// In Fiber, repanic=true returns the panic error to Fiber's error handler
	// instead of re-panicking (since Fiber runs handlers in server goroutines).
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	resp := serveFiber(t, mw, "GET", "/panic", nil, func(c fiber.Ctx) error {
		panic("boom")
	})
	// Fiber's default error handler returns 500 for errors
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_PanicRecovery_ErrorType(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	resp := serveFiber(t, mw, "GET", "/panic-err", nil, func(c fiber.Ctx) error {
		panic(fmt.Errorf("original error"))
	})
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_HandlerErrors(t *testing.T) {
	resp := serveFiber(t, testMiddleware, "GET", "/handler-err", nil, func(c fiber.Ctx) error {
		return fiber.NewError(fiber.StatusInternalServerError, "handler failed")
	})
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_HandlerErrorsWithStack(t *testing.T) {
	resp := serveFiber(t, testMiddleware, "GET", "/handler-err-stack", nil, func(c fiber.Ctx) error {
		return &traceway.StackTraceError{
			Err: fmt.Errorf("wrapped error"),
			Frames: []runtime.Frame{
				{Function: "pkg.Func", File: "file.go", Line: 10},
			},
		}
	})
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_AttributesTagsPropagated(t *testing.T) {
	resp := serveFiber(t, testMiddleware, "GET", "/tags", nil, func(c fiber.Ctx) error {
		attributes := GetAttributesFromCtx(c)
		attributes.SetTag("user_id", "42")
		return c.SendString("ok")
	})
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMiddleware_UnmatchedRoute_DefaultSkipped(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRecordUnmatched(false))
	app := fiber.New()
	app.Use(mw)
	// No route registered for /unmatched

	req := httptest.NewRequest("GET", "/unmatched", nil)
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for unmatched route, got %d", resp.StatusCode)
	}
}

func TestMiddleware_UnmatchedRoute_Recorded(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRecordUnmatched(true))
	app := fiber.New()
	app.Use(mw)
	// No route registered for /unmatched-recorded

	req := httptest.NewRequest("GET", "/unmatched-recorded", nil)
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMiddleware_OnErrorRecording_Url(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingUrl),
	)
	resp := serveFiber(t, mw, "GET", "/error-url", nil, func(c fiber.Ctx) error {
		panic("url test")
	})
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_OnErrorRecording_Query(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingQuery),
	)
	app := fiber.New()
	app.Use(mw)
	app.Get("/error-query", func(c fiber.Ctx) error {
		panic("query test")
	})
	req := httptest.NewRequest("GET", "/error-query?foo=bar", nil)
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_OnErrorRecording_Body(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingBody),
	)
	app := fiber.New()
	app.Use(mw)
	app.Post("/error-body", func(c fiber.Ctx) error {
		panic("body test")
	})
	req := httptest.NewRequest("POST", "/error-body", bytes.NewBufferString(`{"key":"value"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_OnErrorRecording_Headers(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingHeader),
	)
	resp := serveFiber(t, mw, "GET", "/error-headers", nil, func(c fiber.Ctx) error {
		panic("headers test")
	})
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_OnErrorRecording_BodyNonJson(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingBody),
	)
	app := fiber.New()
	app.Use(mw)
	app.Post("/error-body-text", func(c fiber.Ctx) error {
		panic("body non-json test")
	})
	body := bytes.NewBufferString("plain text body")
	req := httptest.NewRequest("POST", "/error-body-text", body)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMiddleware_MultipleRequestsSequential(t *testing.T) {
	app := fiber.New()
	app.Use(testMiddleware)
	app.Get("/seq", func(c fiber.Ctx) error {
		tc := GetTraceFromCtx(c)
		if tc == nil {
			return c.Status(500).SendString("no trace")
		}
		return c.SendString("ok")
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/seq", nil)
		resp, err := app.Test(req, defaultTestConfig)
		if err != nil {
			t.Fatalf("request %d: app.Test: %v", i, err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Errorf("request %d: expected 200, got %d; body=%s", i, resp.StatusCode, string(body))
		}
	}
}

func TestWrapAndExecute_NoPanic(t *testing.T) {
	app := fiber.New()
	var handlerCalled bool
	app.Use(func(c fiber.Ctx) error {
		st, nextErr, panicErr := wrapAndExecute(false, c)
		if st != nil {
			return fmt.Errorf("expected nil stack, got %v", *st)
		}
		if nextErr != nil {
			return fmt.Errorf("expected nil nextErr, got %v", nextErr)
		}
		if panicErr != nil {
			return fmt.Errorf("expected nil panicErr, got %v", panicErr)
		}
		return nil
	})
	app.Get("/wae-ok", func(c fiber.Ctx) error {
		handlerCalled = true
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/wae-ok", nil)
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if !handlerCalled {
		t.Error("handler should have been called")
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestWrapAndExecute_PanicString_Repanic(t *testing.T) {
	app := fiber.New()
	var capturedStack *string
	var capturedPanicErr error
	app.Use(func(c fiber.Ctx) error {
		st, _, panicErr := wrapAndExecute(true, c)
		capturedStack = st
		capturedPanicErr = panicErr
		if panicErr != nil {
			return panicErr
		}
		return nil
	})
	app.Get("/wae-str", func(c fiber.Ctx) error {
		panic("test panic")
	})

	req := httptest.NewRequest("GET", "/wae-str", nil)
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if capturedStack == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if capturedPanicErr == nil {
		t.Fatal("expected non-nil panic error")
	}
	if !strings.Contains(capturedPanicErr.Error(), "test panic") {
		t.Errorf("expected error to contain panic value, got %v", capturedPanicErr)
	}
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestWrapAndExecute_PanicError_Repanic(t *testing.T) {
	app := fiber.New()
	origErr := fmt.Errorf("original error")
	var capturedStack *string
	var capturedPanicErr error
	app.Use(func(c fiber.Ctx) error {
		st, _, panicErr := wrapAndExecute(true, c)
		capturedStack = st
		capturedPanicErr = panicErr
		if panicErr != nil {
			return panicErr
		}
		return nil
	})
	app.Get("/wae-err", func(c fiber.Ctx) error {
		panic(origErr)
	})

	req := httptest.NewRequest("GET", "/wae-err", nil)
	_, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if capturedStack == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if capturedPanicErr != origErr {
		t.Errorf("expected original error preserved, got %v", capturedPanicErr)
	}
}

func TestWrapAndExecute_PanicString_NoRepanic(t *testing.T) {
	app := fiber.New()
	var capturedStack *string
	var capturedPanicErr error
	app.Use(func(c fiber.Ctx) error {
		st, _, panicErr := wrapAndExecute(false, c)
		capturedStack = st
		capturedPanicErr = panicErr
		return nil
	})
	app.Get("/wae-norep", func(c fiber.Ctx) error {
		panic("test panic")
	})

	req := httptest.NewRequest("GET", "/wae-norep", nil)
	resp, err := app.Test(req, defaultTestConfig)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if capturedStack == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if capturedPanicErr != nil {
		t.Errorf("expected nil panic error (no repanic), got %v", capturedPanicErr)
	}
	if resp.StatusCode != 500 {
		t.Errorf("expected status 500, got %d", resp.StatusCode)
	}
}

var _ = errors.New
