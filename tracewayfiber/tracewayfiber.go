package tracewayfiber

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
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

func wrapAndExecute(repanic bool, c fiber.Ctx) (stackTrace *string, nextErr error, panicErr error) {
	defer func() {
		if r := recover(); r != nil {
			m := traceway.FormatRWithStack(r, traceway.CaptureStack(2))
			stackTrace = &m

			if repanic {
				switch v := r.(type) {
				case error:
					panicErr = v
				default:
					panicErr = fmt.Errorf("traceway repanic: %v", v)
				}
			} else {
				c.Status(fiber.StatusInternalServerError)
			}
		}
	}()
	nextErr = c.Next()
	return nil, nextErr, nil
}

type RecordingFlag byte

const (
	RecordingUrl RecordingFlag = 1 << iota
	RecordingQuery
	RecordingBody
	RecordingHeader
)

const bodyLimitForReporting = 64 * 1024

type TracewayFiberOptions struct {
	tracewayOpts     []func(*traceway.TracewayOptions)
	repanic          bool
	recordUnmatched  bool
	ignoredPaths     map[string]struct{}
	streamingPaths   map[string]struct{}
	onErrorRecording RecordingFlag
	filter           func(c fiber.Ctx) bool
}

func WithFilter(fn func(c fiber.Ctx) bool) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.filter = fn
	}
}

func WithRecordUnmatched(val bool) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.recordUnmatched = val
	}
}

func WithIgnoredPaths(paths ...string) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		if s.ignoredPaths == nil {
			s.ignoredPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.ignoredPaths[p] = struct{}{}
		}
	}
}

func WithStreamingPaths(paths ...string) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		if s.streamingPaths == nil {
			s.streamingPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.streamingPaths[p] = struct{}{}
		}
	}
}

func WithOnErrorRecording(val RecordingFlag) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.onErrorRecording = val
	}
}

func WithRepanic(val bool) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.repanic = val
	}
}

func WithDebug(val bool) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithDebug(val))
	}
}

func WithMaxCollectionFrames(val int) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMaxCollectionFrames(val))
	}
}

func WithCollectionInterval(val time.Duration) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithCollectionInterval(val))
	}
}

func WithUploadTimeout(val time.Duration) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithUploadTimeout(val))
	}
}

func WithMetricsInterval(val time.Duration) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMetricsInterval(val))
	}
}

func WithVersion(val string) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithVersion(val))
	}
}

func WithServerName(val string) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithServerName(val))
	}
}

func WithSampleRate(val float64) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithSampleRate(val))
	}
}

func WithErrorSampleRate(val float64) func(*TracewayFiberOptions) {
	return func(s *TracewayFiberOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithErrorSampleRate(val))
	}
}

// GetAttributesFromCtx extracts Attributes from Fiber's Locals store.
func GetAttributesFromCtx(c fiber.Ctx) *traceway.Attributes {
	if v := c.Locals(string(traceway.CtxAttributesKey)); v != nil {
		if attr, ok := v.(*traceway.Attributes); ok {
			return attr
		}
	}
	return nil
}

// GetTraceFromCtx extracts TraceContext from Fiber's Locals store.
func GetTraceFromCtx(c fiber.Ctx) *traceway.TraceContext {
	if v := c.Locals(string(traceway.CtxTraceKey)); v != nil {
		if tc, ok := v.(*traceway.TraceContext); ok {
			return tc
		}
	}
	return nil
}

func MarkStream(c fiber.Ctx) {
	if attrs := GetAttributesFromCtx(c); attrs != nil {
		attrs.SetTag(traceway.StreamAttributeKey, "true")
	}
}

func New(connectionString string, options ...func(*TracewayFiberOptions)) fiber.Handler {
	opts := &TracewayFiberOptions{repanic: true, recordUnmatched: false, onErrorRecording: 0}
	for _, o := range options {
		o(opts)
	}

	traceway.Init(connectionString, opts.tracewayOpts...)

	return func(c fiber.Ctx) error {
		if opts.filter != nil && !opts.filter(c) {
			return c.Next()
		}

		routePath := c.Route().Path
		if routePath == "" {
			if !opts.recordUnmatched {
				return c.Next()
			}
			routePath = c.Path()
		}

		if len(opts.ignoredPaths) > 0 {
			if _, ignored := opts.ignoredPaths[routePath]; ignored {
				return c.Next()
			}
		}

		start := time.Now()
		method := c.Method()
		cIP := c.IP()

		tc := &traceway.TraceContext{
			Id: uuid.NewString(),
		}
		attributes := traceway.NewAttributes()

		c.Locals(string(traceway.CtxAttributesKey), attributes)
		c.Locals(string(traceway.CtxTraceKey), tc)

		stackTraceFormatted, nextErr, panicErr := wrapAndExecute(opts.repanic, c)

		// Determine the effective error to return to Fiber's error handler.
		// Unlike net/http, Fiber runs handlers in fasthttp server goroutines,
		// so we return the panic error rather than re-panicking.
		effectiveErr := nextErr
		if panicErr != nil {
			effectiveErr = panicErr
		}

		duration := time.Since(start)
		statusCode := c.Response().StatusCode()
		bodySize := len(c.Response().Body())

		traceEndpoint := method + " " + routePath

		isError := stackTraceFormatted != nil || effectiveErr != nil || statusCode >= 500
		if !traceway.ShouldSample(isError) {
			return effectiveErr
		}

		if _, alreadyMarked := attributes.GetTag(traceway.StreamAttributeKey); !alreadyMarked {
			markStream := false
			if opts.streamingPaths != nil {
				if _, ok := opts.streamingPaths[routePath]; ok {
					markStream = true
				}
			}
			if !markStream {
				markStream = traceway.IsStreamingContentType(c.GetRespHeader("Content-Type")) ||
					traceway.IsWebSocketUpgrade(statusCode, c.GetRespHeader("Upgrade"))
			}
			if markStream {
				attributes.SetTag(traceway.StreamAttributeKey, "true")
			}
		}

		traceway.CaptureTraceWithAttributes(tc, traceEndpoint, duration, start, statusCode, bodySize, cIP, attributes.GetTags())

		hasHandlerError := nextErr != nil && stackTraceFormatted == nil

		if stackTraceFormatted != nil || hasHandlerError {
			exceptionTags := map[string]string{}

			for k, v := range attributes.GetTags() {
				if k != "User-Agent" {
					exceptionTags[k] = v
				}
			}

			exceptionTags["user agent"] = c.Get("User-Agent")

			if opts.onErrorRecording&RecordingUrl > 0 {
				exceptionTags["url"] = c.Path()
			}
			if opts.onErrorRecording&RecordingQuery > 0 {
				queryMap := make(map[string][]string)
				c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
					k := string(key)
					queryMap[k] = append(queryMap[k], string(value))
				})
				if len(queryMap) > 0 {
					if queryJson, err := json.Marshal(queryMap); err == nil {
						exceptionTags["query"] = string(queryJson)
					}
				}
			}
			if opts.onErrorRecording&RecordingBody > 0 && c.Get("Content-Type") == "application/json" {
				body := c.Body()
				if len(body) > bodyLimitForReporting {
					body = body[:bodyLimitForReporting]
				}
				exceptionTags["body"] = string(body)
			}
			if opts.onErrorRecording&RecordingHeader > 0 {
				headerMap := make(map[string][]string)
				c.Request().Header.VisitAll(func(key, value []byte) {
					k := string(key)
					headerMap[k] = append(headerMap[k], string(value))
				})
				if headersJson, err := json.Marshal(headerMap); err == nil {
					exceptionTags["headers"] = string(headersJson)
				}
			}

			if stackTraceFormatted != nil {
				traceway.CaptureTraceExceptionWithAttributes(tc.Id, *stackTraceFormatted, exceptionTags)
			}

			if hasHandlerError {
				embeddedStack := extractStackFromError(nextErr)
				var formatted string
				if embeddedStack != nil {
					formatted = traceway.FormatErrorWithStack(nextErr, embeddedStack)
				} else {
					formatted = formatErrorChain(nextErr)
				}
				traceway.CaptureTraceExceptionWithAttributes(tc.Id, formatted, exceptionTags)
			}
		}

		return effectiveErr
	}
}
