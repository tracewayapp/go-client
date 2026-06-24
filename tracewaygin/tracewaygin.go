package tracewaygin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gin-gonic/gin"
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

func processGinErrors(c *gin.Context, tc *traceway.TraceContext, exceptionTags map[string]string) {
	for _, ginErr := range c.Errors {
		err := ginErr.Err
		embeddedStack := extractStackFromError(err)

		var formatted string
		if embeddedStack != nil {
			formatted = traceway.FormatErrorWithStack(err, embeddedStack)
		} else {
			formatted = formatErrorChain(err)
		}

		traceway.CaptureTraceExceptionWithAttributes(tc.Id, formatted, exceptionTags)
	}
}

func wrapAndExecute(repanic bool, c *gin.Context, routePath string) (s *string, e error) {
	defer func() {
		if r := recover(); r != nil {
			m := traceway.FormatRWithStack(r, traceway.CaptureStack(2))
			s = &m

			if repanic {
				var errFromRecover error
				switch v := r.(type) {
				case error:
					errFromRecover = v
				default:
					errFromRecover = fmt.Errorf("traceway repanic: %v", v)
				}

				e = errFromRecover
			} else {
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}
	}()
	pprof.Do(c.Request.Context(), pprof.Labels("endpoint", routePath), func(labeledCtx context.Context) {
		c.Request = c.Request.WithContext(labeledCtx)
		c.Next()
	})
	return nil, nil
}

type RecordingFlag byte

const (
	RecordingUrl    RecordingFlag = 1 << iota
	RecordingQuery
	RecordingBody
	RecordingHeader
)

const bodyLimitForReporting = int64(64 * 1024)

type TracewayGinOptions struct {
	tracewayOpts     []func(*traceway.TracewayOptions)
	repanic          bool
	recordUnmatched  bool
	recordStatic     bool
	ignoredPaths     map[string]struct{}
	streamingPaths   map[string]struct{}
	onErrorRecording RecordingFlag
}

func WithRecordUnmatched(val bool) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.recordUnmatched = val
	}
}

func WithRecordStatic(val bool) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.recordStatic = val
	}
}

func WithIgnoredPaths(paths ...string) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		if s.ignoredPaths == nil {
			s.ignoredPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.ignoredPaths[p] = struct{}{}
		}
	}
}

func WithStreamingPaths(paths ...string) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		if s.streamingPaths == nil {
			s.streamingPaths = make(map[string]struct{}, len(paths))
		}
		for _, p := range paths {
			s.streamingPaths[p] = struct{}{}
		}
	}
}

func WithOnErrorRecording(val RecordingFlag) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.onErrorRecording = val
	}
}

func WithRepanic(val bool) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.repanic = val
	}
}
func WithDebug(val bool) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithDebug(val))
	}
}
func WithMaxCollectionFrames(val int) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMaxCollectionFrames(val))
	}
}
func WithCollectionInterval(val time.Duration) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithCollectionInterval(val))
	}
}
func WithUploadTimeout(val time.Duration) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithUploadTimeout(val))
	}
}
func WithMetricsInterval(val time.Duration) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithMetricsInterval(val))
	}
}
func WithVersion(val string) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithVersion(val))
	}
}
func WithServerName(val string) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithServerName(val))
	}
}
func WithSampleRate(val float64) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithSampleRate(val))
	}
}
func WithErrorSampleRate(val float64) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.tracewayOpts = append(s.tracewayOpts, traceway.WithErrorSampleRate(val))
	}
}

func isStaticRoute(c *gin.Context) bool {
	handlerName := c.HandlerName()
	return strings.Contains(handlerName, "StaticFile") ||
		strings.Contains(handlerName, "createStaticHandler")
}

func New(connectionString string, options ...func(*TracewayGinOptions)) gin.HandlerFunc {
	opts := &TracewayGinOptions{repanic: true, recordUnmatched: false, onErrorRecording: 0}
	for _, o := range options {
		o(opts)
	}

	traceway.Init(connectionString, opts.tracewayOpts...)

	return func(c *gin.Context) {
		routePath := c.FullPath()
		if routePath == "" {
			if !opts.recordUnmatched {
				c.Next()
				return
			}
			routePath = c.Request.URL.Path
		}

		if !opts.recordStatic && isStaticRoute(c) {
			c.Next()
			return
		}

		if len(opts.ignoredPaths) > 0 {
			if _, ignored := opts.ignoredPaths[routePath]; ignored {
				c.Next()
				return
			}
		}

		start := time.Now()

		method := c.Request.Method
		clientIP := c.ClientIP()

		distributedTraceId := c.GetHeader("traceway-trace-id")

		tc := &traceway.TraceContext{
			Id:                 uuid.NewString(),
			DistributedTraceId: distributedTraceId,
		}

		if distributedTraceId != "" {
			c.Header("traceway-trace-id", distributedTraceId)
		}

		attributes := traceway.NewAttributes()

		ctx := context.WithValue(c.Request.Context(), string(traceway.CtxAttributesKey), attributes)
		ctx = context.WithValue(ctx, string(traceway.CtxTraceKey), tc)
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(traceway.CtxAttributesKey), attributes)
		c.Set(string(traceway.CtxTraceKey), tc)

		stackTraceFormatted, err := wrapAndExecute(opts.repanic, c, routePath)

		if err != nil {
			errForPanic := err
			defer panic(errForPanic)
		}

		duration := time.Since(start)

		statusCode := c.Writer.Status()
		bodySize := c.Writer.Size()

		if bodySize < 0 {
			bodySize = 0
		}

		traceEndpoint := method + " " + routePath

		defer recover()

		isError := stackTraceFormatted != nil || len(c.Errors) > 0 || statusCode >= 500
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
				markStream = traceway.IsStreamingContentType(c.Writer.Header().Get("Content-Type")) ||
					traceway.IsWebSocketUpgrade(statusCode, c.Writer.Header().Get("Upgrade"))
			}
			if markStream {
				attributes.SetTag(traceway.StreamAttributeKey, "true")
			}
		}

		traceway.CaptureTraceWithAttributes(tc, traceEndpoint, duration, start, statusCode, bodySize, clientIP, attributes.GetTags())

		exceptionTags := map[string]string{}

		hasGinContextError := len(c.Errors) > 0 && stackTraceFormatted == nil

		if stackTraceFormatted != nil || hasGinContextError {
			for k, v := range attributes.GetTags() {
				if k != "User-Agent" {
					exceptionTags[k] = v
				}
			}

			exceptionTags["user agent"] = c.Request.UserAgent()

			if opts.onErrorRecording&RecordingUrl > 0 {
				exceptionTags["url"] = c.Request.URL.Path
			}
			if opts.onErrorRecording&RecordingQuery > 0 {
				query := c.Request.URL.Query()
				if len(map[string][]string(query)) > 0 {
					if queryJson, err := json.Marshal(query); err == nil {
						exceptionTags["query"] = string(queryJson)
					}
				}
			}
			if opts.onErrorRecording&RecordingBody > 0 && c.ContentType() == "application/json" {
				limitedBody, err := io.ReadAll(io.LimitReader(c.Request.Body, bodyLimitForReporting))

				if err == nil {
					c.Request.Body = io.NopCloser(io.MultiReader(
						bytes.NewBuffer(limitedBody),
						c.Request.Body,
					))

					exceptionTags["body"] = string(limitedBody)
				}
			}
			if opts.onErrorRecording&RecordingHeader > 0 {
				if headersJson, err := json.Marshal(c.Request.Header); err == nil {
					exceptionTags["headers"] = string(headersJson)
				}
			}
		}

		if stackTraceFormatted != nil {
			traceway.CaptureTraceExceptionWithAttributes(tc.Id, *stackTraceFormatted, exceptionTags)
		}

		if len(c.Errors) > 0 && stackTraceFormatted == nil {
			processGinErrors(c, tc, exceptionTags)
		}

	}
}
