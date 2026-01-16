package tracewaygin

import (
	"time"

	traceway "go.tracewayapp.com"

	"github.com/gin-gonic/gin"
)

}

func New(connectionString string, options ...func(*traceway.TracewayOptions)) gin.HandlerFunc {
	traceway.Init(connectionString, options...)

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
