package tracewayfasthttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"runtime"
	"strings"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func extractStackFromError(err error) []runtime.Frame {
	var stackErr *traceway.StackTraceError
	if errors.As(err, &stackErr) {
		return stackErr.GetStackFrames()
	}
	return nil
}

func formatErrorChain(err error) string {
	var sb strings.Builder
	errType := reflect.TypeOf(err).String()
	fmt.Fprintf(&sb, "%s: %s", errType, err.Error())

	return sb.String()
}

func clientIP(ctx *fasthttp.RequestCtx) string {
	if xff := string(ctx.Request.Header.Peek("X-Forwarded-For")); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := string(ctx.Request.Header.Peek("X-Real-IP")); xri != "" {
		return strings.TrimSpace(xri)
	}
	addr := ctx.RemoteAddr().String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func wrapAndExecute(repanic bool, next fasthttp.RequestHandler, ctx *fasthttp.RequestCtx) (s *string, e error) {
	defer func() {
		if r := recover(); r != nil {
			m := traceway.FormatRWithStack(r, traceway.CaptureStack(2))
			s = &m

			if repanic {
				switch v := r.(type) {
				case error:
					e = v
				default:
					e = fmt.Errorf("traceway repanic: %v", v)
				}
			} else {
				ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			}
		}
	}()
	next(ctx)
	return nil, nil
}

type RecordingFlag byte

const (
	RecordingUrl    RecordingFlag = 1 << iota
	RecordingQuery
	RecordingBody
	RecordingHeader
)

const bodyLimitForReporting = 64 * 1024

type TracewayFasthttpOptions struct {
	tracewayOpts     []func(*traceway.TracewayOptions)
	repanic          bool
	ignoredPaths     map[string]struct{}
	streamingPaths   map[string]struct{}
	onErrorRecording RecordingFlag
	filter           func(ctx *fasthttp.RequestCtx) bool
}

func WithFilter(fn func(ctx *fasthttp.RequestCtx) bool) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.filter = fn
	}
}

func WithIgnoredPaths(paths ...string) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		if s.ignoredPaths == nil {
			s.ignoredPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.ignoredPaths[p] = struct{}{}
		}
	}
}

func WithStreamingPaths(paths ...string) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		if s.streamingPaths == nil {
			s.streamingPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.streamingPaths[p] = struct{}{}
		}
	}
}

func WithOnErrorRecording(val RecordingFlag) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.onErrorRecording = val
	}
}

func WithRepanic(val bool) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.repanic = val
	}
}

func WithDebug(val bool) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithDebug(val))
	}
}

func WithMaxCollectionFrames(val int) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMaxCollectionFrames(val))
	}
}

func WithCollectionInterval(val time.Duration) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithCollectionInterval(val))
	}
}

func WithUploadTimeout(val time.Duration) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithUploadTimeout(val))
	}
}

func WithMetricsInterval(val time.Duration) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMetricsInterval(val))
	}
}

func WithVersion(val string) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithVersion(val))
	}
}

func WithServerName(val string) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithServerName(val))
	}
}

func WithSampleRate(val float64) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithSampleRate(val))
	}
}

func WithErrorSampleRate(val float64) func(*TracewayFasthttpOptions) {
	return func(s *TracewayFasthttpOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithErrorSampleRate(val))
	}
}

// GetAttributesFromCtx extracts Attributes from fasthttp's UserValue store.
func GetAttributesFromCtx(ctx *fasthttp.RequestCtx) *traceway.Attributes {
	if v := ctx.UserValue(string(traceway.CtxAttributesKey)); v != nil {
		if attr, ok := v.(*traceway.Attributes); ok {
			return attr
		}
	}
	return nil
}

// GetTraceFromCtx extracts TraceContext from fasthttp's UserValue store.
func GetTraceFromCtx(ctx *fasthttp.RequestCtx) *traceway.TraceContext {
	if v := ctx.UserValue(string(traceway.CtxTraceKey)); v != nil {
		if tc, ok := v.(*traceway.TraceContext); ok {
			return tc
		}
	}
	return nil
}

func MarkStream(ctx *fasthttp.RequestCtx) {
	if attrs := GetAttributesFromCtx(ctx); attrs != nil {
		attrs.SetTag(traceway.StreamAttributeKey, "true")
	}
}

func New(connectionString string, options ...func(*TracewayFasthttpOptions)) func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	opts := &TracewayFasthttpOptions{repanic: true, onErrorRecording: 0}
	for _, o := range options {
		o(opts)
	}

	traceway.Init(connectionString, opts.tracewayOpts...)

	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			if opts.filter != nil && !opts.filter(ctx) {
				next(ctx)
				return
			}

			routePath := string(ctx.Path())

			if len(opts.ignoredPaths) > 0 {
				if _, ignored := opts.ignoredPaths[routePath]; ignored {
					next(ctx)
					return
				}
			}

			start := time.Now()
			method := string(ctx.Method())
			cIP := clientIP(ctx)

			tc := &traceway.TraceContext{
				Id: uuid.NewString(),
			}
			attributes := traceway.NewAttributes()

			ctx.SetUserValue(string(traceway.CtxAttributesKey), attributes)
			ctx.SetUserValue(string(traceway.CtxTraceKey), tc)

			stackTraceFormatted, err := wrapAndExecute(opts.repanic, next, ctx)

			if err != nil {
				errForPanic := err
				defer panic(errForPanic)
			}

			duration := time.Since(start)
			statusCode := ctx.Response.StatusCode()
			bodySize := len(ctx.Response.Body())

			traceEndpoint := method + " " + routePath

			defer recover()

			isError := stackTraceFormatted != nil || statusCode >= 500
			if !traceway.ShouldSample(isError) {
				return
			}

			if _, alreadyMarked := attributes.GetTag(traceway.StreamAttributeKey); !alreadyMarked {
				markStream := false
				if opts.streamingPaths != nil {
					if _, ok := opts.streamingPaths[routePath]; ok {
						markStream = true
					}
				}
				if !markStream {
					markStream = traceway.IsStreamingContentType(string(ctx.Response.Header.ContentType())) ||
						traceway.IsWebSocketUpgrade(statusCode, string(ctx.Response.Header.Peek("Upgrade")))
				}
				if markStream {
					attributes.SetTag(traceway.StreamAttributeKey, "true")
				}
			}

			traceway.CaptureTraceWithAttributes(tc, traceEndpoint, duration, start, statusCode, bodySize, cIP, attributes.GetTags())

			if stackTraceFormatted != nil {
				exceptionTags := map[string]string{}

				for k, v := range attributes.GetTags() {
					if k != "User-Agent" {
						exceptionTags[k] = v
					}
				}

				exceptionTags["user agent"] = string(ctx.Request.Header.UserAgent())

				if opts.onErrorRecording&RecordingUrl > 0 {
					exceptionTags["url"] = routePath
				}
				if opts.onErrorRecording&RecordingQuery > 0 {
					queryMap := make(map[string][]string)
					ctx.QueryArgs().VisitAll(func(key, value []byte) {
						k := string(key)
						queryMap[k] = append(queryMap[k], string(value))
					})
					if len(queryMap) > 0 {
						if queryJson, err := json.Marshal(queryMap); err == nil {
							exceptionTags["query"] = string(queryJson)
						}
					}
				}
				if opts.onErrorRecording&RecordingBody > 0 && string(ctx.Request.Header.ContentType()) == "application/json" {
					body := ctx.Request.Body()
					if len(body) > bodyLimitForReporting {
						body = body[:bodyLimitForReporting]
					}
					exceptionTags["body"] = string(body)
				}
				if opts.onErrorRecording&RecordingHeader > 0 {
					headerMap := make(map[string][]string)
					ctx.Request.Header.VisitAll(func(key, value []byte) {
						k := string(key)
						headerMap[k] = append(headerMap[k], string(value))
					})
					if headersJson, err := json.Marshal(headerMap); err == nil {
						exceptionTags["headers"] = string(headersJson)
					}
				}

				traceway.CaptureTraceExceptionWithAttributes(tc.Id, *stackTraceFormatted, exceptionTags)
			}
		}
	}
}
