package tracewayhttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/google/uuid"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type RecordingFlag byte

const (
	RecordingUrl    RecordingFlag = 1 << iota
	RecordingQuery
	RecordingBody
	RecordingHeader
)

const bodyLimitForReporting = int64(64 * 1024)

type TracewayHttpOptions struct {
	tracewayOpts     []func(*traceway.TracewayOptions)
	repanic          bool
	ignoredPaths     map[string]struct{}
	onErrorRecording RecordingFlag
	filter           func(*http.Request) bool
}

func WithFilter(fn func(*http.Request) bool) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.filter = fn
	}
}

func WithIgnoredPaths(paths ...string) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		if s.ignoredPaths == nil {
			s.ignoredPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.ignoredPaths[p] = struct{}{}
		}
	}
}

func WithOnErrorRecording(val RecordingFlag) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.onErrorRecording = val
	}
}

func WithRepanic(val bool) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.repanic = val
	}
}

func WithDebug(val bool) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithDebug(val))
	}
}

func WithMaxCollectionFrames(val int) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMaxCollectionFrames(val))
	}
}

func WithCollectionInterval(val time.Duration) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithCollectionInterval(val))
	}
}

func WithUploadTimeout(val time.Duration) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithUploadTimeout(val))
	}
}

func WithMetricsInterval(val time.Duration) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMetricsInterval(val))
	}
}

func WithVersion(val string) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithVersion(val))
	}
}

func WithServerName(val string) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithServerName(val))
	}
}
func WithSampleRate(val float64) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithSampleRate(val))
	}
}
func WithErrorSampleRate(val float64) func(*TracewayHttpOptions) {
	return func(s *TracewayHttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithErrorSampleRate(val))
	}
}

func wrapAndExecute(repanic bool, next http.Handler, w *responseWriter, r *http.Request) (s *string, e error) {
	defer func() {
		if rec := recover(); rec != nil {
			m := traceway.FormatRWithStack(rec, traceway.CaptureStack(2))
			s = &m

			if repanic {
				switch v := rec.(type) {
				case error:
					e = v
				default:
					e = fmt.Errorf("traceway repanic: %v", rec)
				}
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
	}()
	next.ServeHTTP(w, r)
	return nil, nil
}

func New(connectionString string, options ...func(*TracewayHttpOptions)) func(http.Handler) http.Handler {
	opts := &TracewayHttpOptions{repanic: true, onErrorRecording: 0}
	for _, o := range options {
		o(opts)
	}

	traceway.Init(connectionString, opts.tracewayOpts...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if opts.filter != nil && !opts.filter(r) {
				next.ServeHTTP(w, r)
				return
			}

			routePath := r.URL.Path

			if len(opts.ignoredPaths) > 0 {
				if _, ignored := opts.ignoredPaths[routePath]; ignored {
					next.ServeHTTP(w, r)
					return
				}
			}

			start := time.Now()
			method := r.Method
			cIP := clientIP(r)

			tc := &traceway.TraceContext{
				Id: uuid.NewString(),
			}
			attributes := traceway.NewAttributes()

			ctx := context.WithValue(r.Context(), string(traceway.CtxAttributesKey), attributes)
			ctx = context.WithValue(ctx, string(traceway.CtxTraceKey), tc)
			r = r.WithContext(ctx)

			rw := newResponseWriter(w)

			stackTraceFormatted, err := wrapAndExecute(opts.repanic, next, rw, r)

			if err != nil {
				errForPanic := err
				defer panic(errForPanic)
			}

			duration := time.Since(start)
			statusCode := rw.statusCode
			bodySize := rw.bytesWritten

			traceEndpoint := method + " " + routePath

			defer recover()

			isError := stackTraceFormatted != nil || statusCode >= 500
			if !traceway.ShouldSample(isError) {
				return
			}

			traceway.CaptureTraceWithAttributes(tc, traceEndpoint, duration, start, statusCode, bodySize, cIP, attributes.GetTags())

			if stackTraceFormatted != nil {
				exceptionTags := map[string]string{}

				for k, v := range attributes.GetTags() {
					if k != "User-Agent" {
						exceptionTags[k] = v
					}
				}

				exceptionTags["user agent"] = r.UserAgent()

				if opts.onErrorRecording&RecordingUrl > 0 {
					exceptionTags["url"] = r.URL.Path
				}
				if opts.onErrorRecording&RecordingQuery > 0 {
					query := r.URL.Query()
					if len(map[string][]string(query)) > 0 {
						if queryJson, err := json.Marshal(query); err == nil {
							exceptionTags["query"] = string(queryJson)
						}
					}
				}
				if opts.onErrorRecording&RecordingBody > 0 && r.Header.Get("Content-Type") == "application/json" {
					limitedBody, err := io.ReadAll(io.LimitReader(r.Body, bodyLimitForReporting))
					if err == nil {
						r.Body = io.NopCloser(io.MultiReader(
							bytes.NewBuffer(limitedBody),
							r.Body,
						))
						exceptionTags["body"] = string(limitedBody)
					}
				}
				if opts.onErrorRecording&RecordingHeader > 0 {
					if headersJson, err := json.Marshal(r.Header); err == nil {
						exceptionTags["headers"] = string(headersJson)
					}
				}

				traceway.CaptureTraceExceptionWithAttributes(tc.Id, *stackTraceFormatted, exceptionTags)
			}
		})
	}
}
