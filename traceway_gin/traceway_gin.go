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

func processGinErrors(c *gin.Context, txn *traceway.TransactionContext, exceptionTags map[string]string) {
	for _, ginErr := range c.Errors {
		err := ginErr.Err
		embeddedStack := extractStackFromError(err)

		var formatted string
		if embeddedStack != nil {
			formatted = traceway.FormatErrorWithStack(err, embeddedStack)
		} else {
			formatted = formatErrorChain(err)
		}

		traceway.CaptureTransactionExceptionWithScope(txn.Id, formatted, exceptionTags)
	}
}

func wrapAndExecute(repanic bool, c *gin.Context) (s *string) {
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
					errFromRecover = fmt.Errorf("%v", v)
				}
				panic(traceway.PanicError{Value: errFromRecover, Stack: m})
			} else {
				// we don't propagate just report
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}
	}()
	c.Next()
	return nil
}

type RecordingFlag byte

const (
	RecordingUrl    RecordingFlag = 1 << iota // 0b0001
	RecordingQuery                            // 0b0010
	RecordingBody                             // 0b0100
	RecordingHeader                           // 0b1000
)

const bodyLimitForReporting = int64(64 * 1024)

type TracewayGinOptions struct {
	tracewayOpts     []func(*traceway.TracewayOptions)
	repanic          bool
	recordUnmatched  bool
	onErrorRecording RecordingFlag
}

func WithRecordUnmatched(val bool) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.recordUnmatched = val
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
				// unmatched routes are not recorded based on config
				c.Next()
				return
			}
			// we'll fallback to the actual path
			routePath = c.Request.URL.Path
		}

		start := time.Now()

		method := c.Request.Method
		clientIP := c.ClientIP()

		txn := &traceway.TransactionContext{
			Id: uuid.NewString(),
		}

		scope := traceway.NewScope()

		ctx := context.WithValue(c.Request.Context(), string(traceway.CtxScopeKey), scope)
		ctx = context.WithValue(ctx, string(traceway.CtxTransactionKey), txn)
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(traceway.CtxScopeKey), scope)
		c.Set(string(traceway.CtxTransactionKey), txn)

		stackTraceFormatted := wrapAndExecute(opts.repanic, c)

		duration := time.Since(start)

		statusCode := c.Writer.Status()
		bodySize := c.Writer.Size()

		if bodySize < 0 {
			bodySize = 0
		}

		transactionEndpoint := method + " " + routePath

		defer recover()

		traceway.CaptureTransactionWithScope(txn, transactionEndpoint, duration, start, statusCode, bodySize, clientIP, scope.GetTags())

		exceptionTags := map[string]string{}

		hasGinContextError := len(c.Errors) > 0 && stackTraceFormatted == nil

		if stackTraceFormatted != nil || hasGinContextError {
			for k, v := range scope.GetTags() {
				if k != "User-Agent" { // we add user-agent as a separate tag
					exceptionTags[k] = v
				}
			}

			exceptionTags["user agent"] = c.Request.UserAgent() // we'll only store the user agent IF an exception happens

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
					// restore what we read + whatever remains unread
					c.Request.Body = io.NopCloser(io.MultiReader(
						bytes.NewBuffer(limitedBody),
						c.Request.Body, // the rest of the body
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
			traceway.CaptureTransactionExceptionWithScope(txn.Id, *stackTraceFormatted, exceptionTags)
		}

		if len(c.Errors) > 0 && stackTraceFormatted == nil {
			processGinErrors(c, txn, exceptionTags)
		}
	}
}
