// Package tracewaychi provides traceway middleware for chi routers.
// Since chi is fully compatible with net/http, this package re-exports
// everything from tracewayhttp.
package tracewaychi

import (
	"net/http"

	"go.tracewayapp.com/tracewayhttp"
)

type RecordingFlag = tracewayhttp.RecordingFlag

const (
	RecordingUrl    = tracewayhttp.RecordingUrl
	RecordingQuery  = tracewayhttp.RecordingQuery
	RecordingBody   = tracewayhttp.RecordingBody
	RecordingHeader = tracewayhttp.RecordingHeader
)

type TracewayChiOptions = tracewayhttp.TracewayHttpOptions

var (
	WithFilter             = tracewayhttp.WithFilter
	WithIgnoredPaths       = tracewayhttp.WithIgnoredPaths
	WithStreamingPaths     = tracewayhttp.WithStreamingPaths
	WithOnErrorRecording   = tracewayhttp.WithOnErrorRecording
	WithRepanic            = tracewayhttp.WithRepanic
	WithDebug              = tracewayhttp.WithDebug
	WithMaxCollectionFrames = tracewayhttp.WithMaxCollectionFrames
	WithCollectionInterval = tracewayhttp.WithCollectionInterval
	WithUploadTimeout      = tracewayhttp.WithUploadTimeout
	WithMetricsInterval    = tracewayhttp.WithMetricsInterval
	WithVersion            = tracewayhttp.WithVersion
	WithServerName         = tracewayhttp.WithServerName
	WithSampleRate         = tracewayhttp.WithSampleRate
	WithErrorSampleRate    = tracewayhttp.WithErrorSampleRate
)

func New(connectionString string, options ...func(*TracewayChiOptions)) func(http.Handler) http.Handler {
	return tracewayhttp.New(connectionString, options...)
}
