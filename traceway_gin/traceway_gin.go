package tracewaygin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	tracewayOpts    []func(*traceway.TracewayOptions)
	repanic         bool
	recordUnmatched bool
	recording       RecordingFlag
}

func WithRecordUnmatched(val bool) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.recordUnmatched = val
	}
}

func WithRecording(val RecordingFlag) func(*TracewayGinOptions) {
	return func(s *TracewayGinOptions) {
		s.recording = val
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
	opts := &TracewayGinOptions{repanic: true, recordUnmatched: false, recording: 0}
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

		if opts.recording&RecordingUrl > 0 {
			scope.SetTag("url", c.Request.URL.Path)
		}
		if opts.recording&RecordingQuery > 0 {
			scope.SetTagJson("query params", c.Request.URL.Query())
		}
		if opts.recording&RecordingBody > 0 && c.ContentType() == "application/json" {
			limitedBody, err := io.ReadAll(io.LimitReader(c.Request.Body, bodyLimitForReporting))

			if err == nil {
				// restore what we read + whatever remains unread
				c.Request.Body = io.NopCloser(io.MultiReader(
					bytes.NewBuffer(limitedBody),
					c.Request.Body, // the rest of the body
				))

				scope.SetTagJson("body", string(limitedBody))
			}
		}
		if opts.recording&RecordingHeader > 0 {
			scope.SetTagJson("headers", c.Request.Header)
		}

		traceway.CaptureTransactionWithScope(txn, transactionEndpoint, duration, start, statusCode, bodySize, clientIP, scope.GetTags())

		if stackTraceFormatted != nil {
			exceptionTags := scope.GetTags()
			exceptionTags["user_agent"] = c.Request.UserAgent() // we'll only store the user agent IF an exception happens
			traceway.CaptureTransactionExceptionWithScope(txn.Id, *stackTraceFormatted, exceptionTags)
		}
	}
}
