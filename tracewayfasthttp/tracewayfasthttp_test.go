package tracewayfasthttp

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

var testMiddleware func(next fasthttp.RequestHandler) fasthttp.RequestHandler

func TestMain(m *testing.M) {
	testMiddleware = New("testtoken@http://localhost:19876/noop")
	os.Exit(m.Run())
}

func serveFasthttp(t *testing.T, mw func(next fasthttp.RequestHandler) fasthttp.RequestHandler, method, path string, body []byte, handler fasthttp.RequestHandler) (statusCode int, respBody []byte) {
	t.Helper()

	h := mw(handler)

	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	go func() {
		if err := fasthttp.Serve(ln, h); err != nil {
			// listener closed is expected
		}
	}()

	conn, err := ln.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://test" + path)
	req.Header.SetMethod(method)
	if body != nil {
		req.SetBody(body)
	}

	bw := bufio.NewWriter(conn)
	if err := req.Write(bw); err != nil {
		t.Fatalf("write request: %v", err)
	}
	bw.Flush()

	br := bufio.NewReader(conn)
	if err := resp.Read(br); err != nil {
		t.Fatalf("read response: %v", err)
	}

	return resp.StatusCode(), append([]byte(nil), resp.Body()...)
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

func TestClientIP_XForwardedFor_Single(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := clientIP(ctx); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XForwardedFor_Multiple(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if ip := clientIP(ctx); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XRealIP(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("X-Real-IP", "9.8.7.6")
	if ip := clientIP(ctx); ip != "9.8.7.6" {
		t.Errorf("expected 9.8.7.6, got %s", ip)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	// RemoteAddr() returns the Addr from the connection; for a bare ctx it returns 0.0.0.0:0
	ip := clientIP(ctx)
	if ip == "" {
		t.Error("expected non-empty IP")
	}
}

func TestClientIP_Priority(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("X-Forwarded-For", "1.1.1.1")
	ctx.Request.Header.Set("X-Real-IP", "2.2.2.2")
	if ip := clientIP(ctx); ip != "1.1.1.1" {
		t.Errorf("expected X-Forwarded-For to win, got %s", ip)
	}
}

func TestWithRepanic(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithRepanic(true)(opts)
	if !opts.repanic {
		t.Error("expected repanic=true")
	}
	WithRepanic(false)(opts)
	if opts.repanic {
		t.Error("expected repanic=false")
	}
}

func TestWithFilter(t *testing.T) {
	called := false
	fn := func(*fasthttp.RequestCtx) bool { called = true; return true }
	opts := &TracewayFasthttpOptions{}
	WithFilter(fn)(opts)
	if opts.filter == nil {
		t.Fatal("expected filter to be set")
	}
	opts.filter(&fasthttp.RequestCtx{})
	if !called {
		t.Error("expected filter function to be called")
	}
}

func TestWithIgnoredPaths(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
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
	opts := &TracewayFasthttpOptions{}
	WithOnErrorRecording(RecordingUrl | RecordingBody)(opts)
	if opts.onErrorRecording != RecordingUrl|RecordingBody {
		t.Errorf("expected flags 0b0101, got %08b", opts.onErrorRecording)
	}
}

func TestWithDebug(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithDebug(true)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMaxCollectionFrames(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithMaxCollectionFrames(20)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithCollectionInterval(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithCollectionInterval(10 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithUploadTimeout(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithUploadTimeout(5 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithMetricsInterval(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithMetricsInterval(60 * time.Second)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithVersion(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithVersion("1.0.0")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithServerName(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithServerName("my-server")(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithSampleRate(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithSampleRate(0.5)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestWithErrorSampleRate(t *testing.T) {
	opts := &TracewayFasthttpOptions{}
	WithErrorSampleRate(0.25)(opts)
	if len(opts.tracewayOpts) != 1 {
		t.Error("expected one traceway option appended")
	}
}

func TestMiddleware_NormalRequest(t *testing.T) {
	code, body := serveFasthttp(t, testMiddleware, "GET", "/hello", nil, func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("OK")
	})
	if code != 200 {
		t.Errorf("expected 200, got %d", code)
	}
	if string(body) != "OK" {
		t.Errorf("expected body 'OK', got %q", string(body))
	}
}

func TestMiddleware_CustomStatusCode(t *testing.T) {
	code, _ := serveFasthttp(t, testMiddleware, "GET", "/notfound", nil, func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(404)
		ctx.SetBodyString("not found")
	})
	if code != 404 {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestMiddleware_ContextHasAttributesAndTrace(t *testing.T) {
	code, body := serveFasthttp(t, testMiddleware, "GET", "/ctx", nil, func(ctx *fasthttp.RequestCtx) {
		attributes := GetAttributesFromCtx(ctx)
		tc := GetTraceFromCtx(ctx)
		if attributes == nil {
			ctx.SetStatusCode(500)
			ctx.SetBodyString("attributes nil")
			return
		}
		if tc == nil {
			ctx.SetStatusCode(500)
			ctx.SetBodyString("trace nil")
			return
		}
		if tc.Id == "" {
			ctx.SetStatusCode(500)
			ctx.SetBodyString("trace id empty")
			return
		}
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	if code != 200 {
		t.Errorf("expected 200, got %d; body=%s", code, string(body))
	}
}

func TestMiddleware_IgnoredPathSkipsRecording(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithIgnoredPaths("/health"))
	called := false
	code, _ := serveFasthttp(t, mw, "GET", "/health", nil, func(ctx *fasthttp.RequestCtx) {
		called = true
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	if !called {
		t.Error("handler should still be called for ignored paths")
	}
	if code != 200 {
		t.Errorf("expected 200, got %d", code)
	}
}

func TestMiddleware_FilterRejectSkips(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithFilter(func(ctx *fasthttp.RequestCtx) bool {
		return false
	}))
	called := false
	code, _ := serveFasthttp(t, mw, "GET", "/filtered", nil, func(ctx *fasthttp.RequestCtx) {
		called = true
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	if !called {
		t.Error("handler should be called even when filter rejects")
	}
	if code != 200 {
		t.Errorf("expected 200, got %d", code)
	}
}

func TestMiddleware_FilterAcceptRecords(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithFilter(func(ctx *fasthttp.RequestCtx) bool {
		return true
	}))
	code, body := serveFasthttp(t, mw, "GET", "/accepted", nil, func(ctx *fasthttp.RequestCtx) {
		attributes := GetAttributesFromCtx(ctx)
		tc := GetTraceFromCtx(ctx)
		if attributes == nil || tc == nil {
			ctx.SetStatusCode(500)
			ctx.SetBodyString("missing context values")
			return
		}
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	if code != 200 {
		t.Errorf("expected 200, got %d; body=%s", code, string(body))
	}
}

func TestMiddleware_PanicRecovery_NoRepanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(false))
	code, _ := serveFasthttp(t, mw, "GET", "/panic", nil, func(ctx *fasthttp.RequestCtx) {
		panic("boom")
	})
	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
}

func TestMiddleware_PanicRecovery_Repanic(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	// When repanic is true, the handler panics which causes the server goroutine to crash.
	// We test this by constructing a RequestCtx directly.
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("/panic")
	ctx.Request.Header.SetMethod("GET")

	handler := mw(func(ctx *fasthttp.RequestCtx) {
		panic("boom")
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate")
		}
	}()
	handler(ctx)
	t.Fatal("should not reach here")
}

func TestMiddleware_PanicRecovery_ErrorType(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop", WithRepanic(true))
	origErr := fmt.Errorf("original error")

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("/panic-err")
	ctx.Request.Header.SetMethod("GET")

	handler := mw(func(ctx *fasthttp.RequestCtx) {
		panic(origErr)
	})

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
	handler(ctx)
	t.Fatal("should not reach here")
}

func TestMiddleware_OnErrorRecording_Url(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingUrl),
	)
	code, _ := serveFasthttp(t, mw, "GET", "/error-url", nil, func(ctx *fasthttp.RequestCtx) {
		panic("url test")
	})
	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
}

func TestMiddleware_OnErrorRecording_Query(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingQuery),
	)
	code, _ := serveFasthttp(t, mw, "GET", "/error-query?foo=bar", nil, func(ctx *fasthttp.RequestCtx) {
		panic("query test")
	})
	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
}

func TestMiddleware_OnErrorRecording_Body(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingBody),
	)

	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	handler := mw(func(ctx *fasthttp.RequestCtx) {
		panic("body test")
	})

	go func() {
		fasthttp.Serve(ln, handler)
	}()

	conn, err := ln.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://test/error-body")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	req.SetBodyString(`{"key":"value"}`)

	bw := bufio.NewWriter(conn)
	req.Write(bw)
	bw.Flush()

	br := bufio.NewReader(conn)
	resp.Read(br)

	if resp.StatusCode() != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode())
	}
}

func TestMiddleware_OnErrorRecording_Headers(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingHeader),
	)
	code, _ := serveFasthttp(t, mw, "GET", "/error-headers", nil, func(ctx *fasthttp.RequestCtx) {
		panic("headers test")
	})
	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
}

func TestMiddleware_OnErrorRecording_BodyNonJson(t *testing.T) {
	mw := New("testtoken@http://localhost:19876/noop",
		WithRepanic(false),
		WithOnErrorRecording(RecordingBody),
	)

	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	handler := mw(func(ctx *fasthttp.RequestCtx) {
		panic("body non-json test")
	})

	go func() {
		fasthttp.Serve(ln, handler)
	}()

	conn, err := ln.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://test/error-body-text")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("text/plain")
	req.SetBodyString("plain text body")

	bw := bufio.NewWriter(conn)
	req.Write(bw)
	bw.Flush()

	br := bufio.NewReader(conn)
	resp.Read(br)

	if resp.StatusCode() != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode())
	}
}

func TestMiddleware_MultipleRequestsSequential(t *testing.T) {
	for i := 0; i < 3; i++ {
		code, body := serveFasthttp(t, testMiddleware, "GET", "/seq", nil, func(ctx *fasthttp.RequestCtx) {
			tc := GetTraceFromCtx(ctx)
			if tc == nil {
				ctx.SetStatusCode(500)
				ctx.SetBodyString("no trace")
				return
			}
			ctx.SetStatusCode(200)
			ctx.SetBodyString("ok")
		})
		if code != 200 {
			t.Errorf("request %d: expected 200, got %d; body=%s", i, code, string(body))
		}
	}
}

func TestWrapAndExecute_NoPanic(t *testing.T) {
	var handlerCalled bool
	ctx := &fasthttp.RequestCtx{}
	s, e := wrapAndExecute(false, func(ctx *fasthttp.RequestCtx) {
		handlerCalled = true
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	}, ctx)
	if !handlerCalled {
		t.Error("handler should have been called")
	}
	if s != nil {
		t.Errorf("expected nil stack, got %v", *s)
	}
	if e != nil {
		t.Errorf("expected nil error, got %v", e)
	}
}

func TestWrapAndExecute_PanicString_Repanic(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	s, e := wrapAndExecute(true, func(ctx *fasthttp.RequestCtx) {
		panic("test panic")
	}, ctx)
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
	ctx := &fasthttp.RequestCtx{}
	origErr := fmt.Errorf("original error")
	s, e := wrapAndExecute(true, func(ctx *fasthttp.RequestCtx) {
		panic(origErr)
	}, ctx)
	if s == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if e != origErr {
		t.Errorf("expected original error preserved, got %v", e)
	}
}

func TestWrapAndExecute_PanicString_NoRepanic(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	s, e := wrapAndExecute(false, func(ctx *fasthttp.RequestCtx) {
		panic("test panic")
	}, ctx)
	if s == nil {
		t.Fatal("expected non-nil stack trace")
	}
	if e != nil {
		t.Errorf("expected nil error (no repanic), got %v", e)
	}
	if ctx.Response.StatusCode() != 500 {
		t.Errorf("expected status 500, got %d", ctx.Response.StatusCode())
	}
}

var _ = errors.New
var _ = net.SplitHostPort
