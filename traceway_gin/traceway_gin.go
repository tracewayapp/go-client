package tracewaygin

import (
	"context"
	"net/http"
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func wrapAndExecute(c *gin.Context) (s *string) {
	defer func() {
		if r := recover(); r != nil {
			m := traceway.FormatRWithStack(r, traceway.CaptureStack(2))
			s = &m
			// we don't propagate just report
			// TODO: This should be configurable
			c.AbortWithStatus(http.StatusInternalServerError)
		}
	}()
	c.Next()
	return nil
}

type TracewayGinOptions struct {
	tracewayOpts []func(*traceway.TracewayOptions)
	repanic      bool
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
	opts := &TracewayGinOptions{repanic: true}
	for _, o := range options {
		o(opts)
	}

	traceway.Init(connectionString, opts.tracewayOpts...)

	return func(c *gin.Context) {
		start := time.Now()

		method := c.Request.Method
		clientIP := c.ClientIP()

		// Create transaction context
		txn := &traceway.TransactionContext{
			Id: uuid.NewString(),
		}

		// Create request-scoped scope with defaults
		scope := traceway.NewScope()

		// Store scope and transaction in both gin.Context and request context
		ctx := context.WithValue(c.Request.Context(), string(traceway.CtxScopeKey), scope)
		ctx = context.WithValue(ctx, string(traceway.CtxTransactionKey), txn)
		c.Request = c.Request.WithContext(ctx)
		c.Set(string(traceway.CtxScopeKey), scope)
		c.Set(string(traceway.CtxTransactionKey), txn)

		stackTraceFormatted := wrapAndExecute(c)

		duration := time.Since(start)

		statusCode := c.Writer.Status()
		bodySize := c.Writer.Size()

		if bodySize < 0 {
			bodySize = 0
		}

		// Use the registered route pattern (e.g., /users/:id) instead of actual path
		routePath := c.FullPath()
		if routePath == "" {
			// Fallback to actual path for unmatched routes
			routePath = c.Request.URL.Path
		}

		transactionEndpoint := method + " " + routePath

		defer recover()

		// Capture transaction with scope
		traceway.CaptureTransactionWithScope(txn, transactionEndpoint, duration, start, statusCode, bodySize, clientIP, scope.GetTags())

		if stackTraceFormatted != nil {
			exceptionTags := scope.GetTags()
			exceptionTags["user_agent"] = c.Request.UserAgent() // we'll only store the user agent IF an exception happens
			traceway.CaptureTransactionExceptionWithScope(txn.Id, *stackTraceFormatted, exceptionTags)
		}
	}
}
