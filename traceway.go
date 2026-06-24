package traceway

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.tracewayapp.com/metrics/system"

	"github.com/google/uuid"
)

type tracewayContextKey string

const CtxAttributesKey tracewayContextKey = "TRACEWAY_ATTRIBUTES"
const CtxTraceKey tracewayContextKey = "TRACEWAY_TRACE"

const StreamAttributeKey = "traceway.is_stream"

type Attributes struct {
	tags map[string]string
	mu   sync.RWMutex
}

func NewAttributes() *Attributes {
	return &Attributes{tags: make(map[string]string)}
}

func (s *Attributes) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[key] = value
}

func (s *Attributes) SetTagJson(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	valueStr, err := json.Marshal(value)

	if err != nil {
		return err
	}

	s.tags[key] = string(valueStr)

	return nil
}

func (s *Attributes) GetTag(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.tags[key]
	return val, ok
}

func (s *Attributes) GetTags() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		result[k] = v
	}
	return result
}

func (s *Attributes) Clone() *Attributes {
	return &Attributes{tags: s.GetTags()}
}

func (s *Attributes) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags = make(map[string]string)
}

var defaultAttributes = NewAttributes()

type TraceContext struct {
	Id                 string
	IsTask             bool
	DistributedTraceId string
	Spans              []Span
	mu                 sync.Mutex
}

func (t *TraceContext) AddSpan(span Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Spans = append(t.Spans, span)
}

func (t *TraceContext) GetSpans() []Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Spans
}

func ConfigureAttributes(fn func(*Attributes)) {
	fn(defaultAttributes)
}

func WithAttributes(ctx context.Context, fn func(*Attributes)) context.Context {
	attributes := GetAttributesFromContext(ctx).Clone()
	fn(attributes)
	return context.WithValue(ctx, CtxAttributesKey, attributes)
}

func GetAttributesFromContext(ctx context.Context) *Attributes {
	if ctx == nil {
		return defaultAttributes.Clone()
	}
	if attributes, ok := ctx.Value(string(CtxAttributesKey)).(*Attributes); ok && attributes != nil {
		return attributes
	}

	return defaultAttributes.Clone()
}

func GetTraceFromContext(ctx context.Context) *TraceContext {
	if val, ok := ctx.Value(string(CtxTraceKey)).(*TraceContext); ok {
		return val
	}
	return nil
}

func GetTraceIdFromContext(ctx context.Context) *string {
	if tc := GetTraceFromContext(ctx); tc != nil {
		return &tc.Id
	}
	return nil
}

func InjectDistributedTraceHeaders(ctx context.Context, req *http.Request) {
	tc := GetTraceFromContext(ctx)
	if tc != nil && tc.DistributedTraceId != "" {
		req.Header.Set("traceway-trace-id", tc.DistributedTraceId)
	}
}

func GetIsTaskFromContext(ctx context.Context) bool {
	if tc := GetTraceFromContext(ctx); tc != nil {
		return tc.IsTask
	}
	return false
}

func StartTrace(ctx context.Context) context.Context {
	tc := &TraceContext{
		Id:     uuid.NewString(),
		IsTask: false,
	}

	attributes := GetAttributesFromContext(ctx)

	ctx = context.WithValue(ctx, CtxTraceKey, tc)
	ctx = context.WithValue(ctx, CtxAttributesKey, attributes)

	return ctx
}

var cachedHostname string
var hostnameOnce sync.Once

func getHostname() string {
	hostnameOnce.Do(func() {
		hostname, err := os.Hostname()
		if err != nil {
			cachedHostname = "unknown"
		} else {
			if strings.Contains(hostname, ".") {
				cachedHostname = hostname[0:strings.Index(hostname, ".")]
			} else {
				cachedHostname = hostname
			}
		}
	})
	return cachedHostname
}

func CaptureStack(skip int) []runtime.Frame {
	const maxDepth = 64
	pcs := make([]uintptr, maxDepth)

	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return nil
	}

	frames := runtime.CallersFrames(pcs[:n])
	result := make([]runtime.Frame, 0, n)

	for {
		frame, more := frames.Next()
		result = append(result, frame)
		if !more {
			break
		}
	}
	return result
}

func FormatRWithStack(r interface{}, frames []runtime.Frame) string {
	var err error
	switch v := r.(type) {
	case error:
		err = v
	default:
		err = fmt.Errorf("%v", v)
	}
	return FormatErrorWithStack(err, frames)
}

func FormatErrorWithStack(err error, frames []runtime.Frame) string {
	var sb strings.Builder

	if err != nil {
		errType := reflect.TypeOf(err).String()
		fmt.Fprintf(&sb, "%s: %s\n", errType, err.Error())
	}

	for _, frame := range frames {
		fn := frame.Function
		if idx := strings.LastIndex(fn, "/"); idx >= 0 {
			fn = fn[idx+1:]
		}
		if idx := strings.Index(fn, "."); idx >= 0 {
			fn = fn[idx+1:]
		}
		fmt.Fprintf(&sb, "%s()\n", fn)
		fmt.Fprintf(&sb, "    %s:%d\n", frame.File, frame.Line)
	}

	return sb.String()
}

type ExceptionStackTrace struct {
	TraceId            *string           `json:"traceId"`
	IsTask             bool              `json:"isTask,omitempty"`
	StackTrace         string            `json:"stackTrace"`
	RecordedAt         time.Time         `json:"recordedAt"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	IsMessage          bool              `json:"isMessage"`
	DistributedTraceId *string           `json:"distributedTraceId,omitempty"`
}

type MetricRecord struct {
	Name       string            `json:"name"`
	Value      float64           `json:"value"`
	RecordedAt time.Time         `json:"recordedAt"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type Trace struct {
	Id                 string            `json:"id"`
	Endpoint           string            `json:"endpoint"`
	Duration           time.Duration     `json:"duration"`
	RecordedAt         time.Time         `json:"recordedAt"`
	StatusCode         int               `json:"statusCode"`
	BodySize           int               `json:"bodySize"`
	ClientIP           string            `json:"clientIP"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	Spans              []Span            `json:"spans,omitempty"`
	IsTask             bool              `json:"isTask,omitempty"`
	DistributedTraceId string            `json:"distributedTraceId,omitempty"`
}

func MarkStream(ctx context.Context) {
	GetAttributesFromContext(ctx).SetTag(StreamAttributeKey, "true")
}

func IsStreamingContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

func IsWebSocketUpgrade(statusCode int, upgradeHeader string) bool {
	if statusCode == http.StatusSwitchingProtocols {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(upgradeHeader), "websocket")
}

type Span struct {
	Id        string        `json:"id"`
	Name      string        `json:"name"`
	StartTime time.Time     `json:"startTime"`
	Duration  time.Duration `json:"duration"`
}

type ActiveSpan struct {
	span      Span
	trace     *TraceContext
	startedAt time.Time
	ended     bool
	mu        sync.Mutex
}

func (s *ActiveSpan) End() {
	if s == nil || s.trace == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return
	}

	s.ended = true
	s.span.Duration = time.Since(s.startedAt)
	s.trace.AddSpan(s.span)
}

type CollectionFrame struct {
	StackTraces []*ExceptionStackTrace `json:"stackTraces"`
	Metrics     []*MetricRecord        `json:"metrics"`
	Traces      []*Trace               `json:"traces"`
}

type collectionFrameMessageType int

const (
	CollectionFrameMessageTypeException   = 0
	CollectionFrameMessageTypeMetric      = 1
	CollectionFrameMessageTypeTrace       = 2
	CollectionFrameMessageTypeClearFrames = 3
)

type CollectionFrameMessage struct {
	msgType                  collectionFrameMessageType
	exceptionStackTrace      *ExceptionStackTrace
	metric                   *MetricRecord
	trace                    *Trace
	collectionFramesToRemove []*CollectionFrame
}

type CollectionFrameStore struct {
	current      *CollectionFrame
	currentSetAt time.Time
	sendQueue    TypedRing[*CollectionFrame, CollectionFrame]

	mu sync.RWMutex

	stopCh chan struct{}
	wg     sync.WaitGroup

	messageQueue chan CollectionFrameMessage

	systemCollector *system.Collector

	lastUploadStarted *time.Time

	apiUrl              string
	token               string
	debug               bool
	maxCollectionFrames int
	collectionInterval  time.Duration
	uploadTimeout       time.Duration
	metricsInterval     time.Duration
	version             string
	serverName          string
	sampleRate          float64
	errorSampleRate     float64
}

func InitCollectionFrameStore(
	apiUrl string,
	token string,
	debug bool,
	maxCollectionFrames int,
	collectionInterval time.Duration,
	uploadTimeout time.Duration,
	metricsInterval time.Duration,
	version string,
	serverName string,
	sampleRate float64,
	errorSampleRate float64,
) *CollectionFrameStore {
	store := &CollectionFrameStore{
		current:      nil,
		currentSetAt: time.Now(),
		sendQueue:    InitTypedRing[*CollectionFrame](maxCollectionFrames),
		stopCh:       make(chan struct{}),
		messageQueue: make(chan CollectionFrameMessage),

		apiUrl: apiUrl,
		token:  token,
		debug:  debug,

		maxCollectionFrames: maxCollectionFrames,
		collectionInterval:  collectionInterval,
		uploadTimeout:       uploadTimeout,
		metricsInterval:     metricsInterval,
		version:             version,
		serverName:          serverName,
		sampleRate:          sampleRate,
		errorSampleRate:     errorSampleRate,
	}

	systemCollector, err := system.NewCollector()
	if err == nil {
		store.systemCollector = systemCollector
	} else if debug {
		log.Println("Traceway: system metrics unavailable:", err)
	}

	store.wg.Add(1)
	go store.process()
	go store.processMetrics()

	return store
}

const (
	MetricNameMemoryUsage  = "mem.used"
	MetricNameMemoryTotal  = "mem.total"
	MetricNameCpuUsage     = "cpu.used_pcnt"
	MetricNameGoRoutines   = "go.go_routines"
	MetricNameHeapObjects  = "go.heap_objects"
	MetricNameNumGC        = "go.num_gc"
	MetricNameGCPauseTotal = "go.gc_pause"
)

func (s *CollectionFrameStore) processMetrics() {
	metricsTicker := time.NewTicker(s.metricsInterval)
	defer metricsTicker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-metricsTicker.C:
			s.safeProcessMetrics()
		}
	}
}

func (s *CollectionFrameStore) safeProcessMetrics() {
	defer func() {
		if r := recover(); r != nil {
			log.Print("Traceway: failed to get metrics data")
		}
	}()
	if s.systemCollector != nil {
		stats, err := s.systemCollector.Read(context.Background())
		if err == nil {
			if stats.HasCpuPercent {
				CaptureMetric(MetricNameCpuUsage, stats.CpuPercent)
			}
			if stats.TotalMemory > 0 {
				CaptureMetric(MetricNameMemoryTotal, float64(stats.TotalMemory)/1024/1024)
			}
		} else {
			if s.debug {
				log.Println("Traceway system metrics not read", err)
			}
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	CaptureMetric(MetricNameMemoryUsage, float64(m.Alloc)/1024/1024)
	CaptureMetric(MetricNameGoRoutines, float64(runtime.NumGoroutine()))
	CaptureMetric(MetricNameHeapObjects, float64(m.HeapObjects))
	CaptureMetric(MetricNameNumGC, float64(m.NumGC))
	CaptureMetric(MetricNameGCPauseTotal, float64(m.PauseTotalNs))
}
func (s *CollectionFrameStore) process() {
	defer s.wg.Done()

	rotationTicker := time.NewTicker(s.collectionInterval)
	defer rotationTicker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-rotationTicker.C:
			if s.current != nil {
				if s.currentSetAt.Before(time.Now().Add(-s.collectionInterval)) {
					s.rotateCurrentCollectionFrame()
					s.processSendQueue()
				}
			} else if s.sendQueue.len > 0 {
				s.processSendQueue()
			}
		case msg := <-s.messageQueue:
			if msg.msgType == CollectionFrameMessageTypeClearFrames {
				s.sendQueue.Remove(msg.collectionFramesToRemove)
				continue
			}

			if s.current == nil {
				s.current = &CollectionFrame{}
				s.currentSetAt = time.Now()
			}

			switch msg.msgType {
			case CollectionFrameMessageTypeException:
				s.current.StackTraces = append(s.current.StackTraces, msg.exceptionStackTrace)
			case CollectionFrameMessageTypeMetric:
				s.current.Metrics = append(s.current.Metrics, msg.metric)
			case CollectionFrameMessageTypeTrace:
				s.current.Traces = append(s.current.Traces, msg.trace)
			}
		}
	}
}
func (s *CollectionFrameStore) rotateCurrentCollectionFrame() {
	s.sendQueue.Push(s.current)
	s.current = nil
}
func (s *CollectionFrameStore) processSendQueue() {
	if s.lastUploadStarted == nil || s.lastUploadStarted.Before(time.Now().Add(s.uploadTimeout)) {
		now := time.Now()
		s.lastUploadStarted = &now
		go s.triggerUpload(s.sendQueue.ReadAll())
	}
}

func (s *CollectionFrameStore) triggerUpload(framesToSend []*CollectionFrame) {
	defer func() {
		if r := recover(); s.debug && r != nil {
			log.Print("Traceway: failed to upload the CollectionFrame")
			log.Println(r)
			debug.PrintStack()
		}
	}()

	jsonData, err := json.Marshal(map[string]interface{}{
		"collectionFrames": framesToSend,
		"appVersion":       s.version,
		"serverName":       s.serverName,
	})
	if err != nil {
		if s.debug {
			log.Printf("Traceway: failed to marshal frames: %v", err)
		}
		return
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(jsonData); err != nil {
		if s.debug {
			log.Printf("Traceway: gz write failed: %v", err)
		}
		return
	}
	if err := gz.Close(); err != nil {
		if s.debug {
			log.Printf("Traceway: gz write failed: %v", err)
		}
		return
	}

	req, err := http.NewRequest("POST", s.apiUrl, &buf)
	if err != nil {
		if s.debug {
			log.Printf("Traceway: failed to create request: %v", err)
		}
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Encoding", "gzip")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		if s.debug {
			log.Printf("Traceway: failed to send request: %v", err)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		s.messageQueue <- CollectionFrameMessage{
			msgType:                  CollectionFrameMessageTypeClearFrames,
			collectionFramesToRemove: framesToSend,
		}
	}
}

var collectionFrameStore *CollectionFrameStore

func PrintCollectionFrameMetrics() {
	val, _ := json.Marshal(collectionFrameStore.current)
	fmt.Println("current", string(val))

	fmt.Println("currentSetAt", collectionFrameStore.currentSetAt)
	fmt.Println("sendQueue", collectionFrameStore.sendQueue)
	fmt.Println("lastUploadStarted", collectionFrameStore.lastUploadStarted)
	fmt.Println("apiUrl", collectionFrameStore.apiUrl)
	fmt.Println("token", collectionFrameStore.token)
	fmt.Println("debug", collectionFrameStore.debug)
	fmt.Println("maxCollectionFrames", collectionFrameStore.maxCollectionFrames)
	fmt.Println("collectionInterval", collectionFrameStore.collectionInterval)
	fmt.Println("uploadTimeout", collectionFrameStore.uploadTimeout)
}

type TracewayOptions struct {
	debug               bool
	maxCollectionFrames int
	collectionInterval  time.Duration
	uploadTimeout       time.Duration
	metricsInterval     time.Duration
	version             string
	serverName          string
	sampleRate          float64
	errorSampleRate     float64
	profilingEnabled    bool
	profilingService    string
	profilingInterval   time.Duration
}

func NewTracewayOptions(options ...func(*TracewayOptions)) *TracewayOptions {
	svr := &TracewayOptions{
		maxCollectionFrames: 12,
		collectionInterval:  5 * time.Second,
		uploadTimeout:       2 * time.Second,
		metricsInterval:     30 * time.Second,
		version:             "",
		serverName:          getHostname(),
		sampleRate:          1,
		errorSampleRate:     1,
		profilingInterval:   defaultProfilingInterval,
	}
	for _, o := range options {
		o(svr)
	}
	return svr
}
func WithDebug(val bool) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.debug = val
	}
}
func WithMaxCollectionFrames(val int) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.maxCollectionFrames = val
	}
}
func WithCollectionInterval(val time.Duration) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.collectionInterval = val
	}
}
func WithUploadTimeout(val time.Duration) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.uploadTimeout = val
	}
}
func WithMetricsInterval(val time.Duration) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.metricsInterval = val
	}
}
func WithVersion(val string) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.version = val
	}
}
func WithServerName(val string) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.serverName = val
	}
}
func WithSampleRate(val float64) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.sampleRate = val
	}
}
func WithErrorSampleRate(val float64) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.errorSampleRate = val
	}
}

func Init(connectionString string, options ...func(*TracewayOptions)) error {
	if collectionFrameStore != nil {
		return fmt.Errorf("Second Traceway initialization detected")
	}
	connParts := strings.SplitN(connectionString, "@", 2)
	if len(connParts) != 2 {
		return fmt.Errorf("traceway: invalid connection string, expected \"<token>@<url>\"")
	}

	token := connParts[0]
	apiUrl := connParts[1]

	tracewayOptions := NewTracewayOptions(options...)

	collectionFrameStore = InitCollectionFrameStore(
		apiUrl,
		token,
		tracewayOptions.debug,
		tracewayOptions.maxCollectionFrames,
		tracewayOptions.collectionInterval,
		tracewayOptions.uploadTimeout,
		tracewayOptions.metricsInterval,
		tracewayOptions.version,
		tracewayOptions.serverName,
		tracewayOptions.sampleRate,
		tracewayOptions.errorSampleRate,
	)

	if tracewayOptions.profilingEnabled {
		if err := startProfiler(apiUrl, token, tracewayOptions); err != nil && tracewayOptions.debug {
			log.Printf("Traceway: profiling disabled: %v", err)
		}
	}

	return nil
}

func ShouldSample(isError bool) bool {
	if collectionFrameStore == nil {
		return false
	}
	rate := collectionFrameStore.sampleRate
	if isError {
		rate = collectionFrameStore.errorSampleRate
	}
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	return rand.Float64() < rate
}

func CaptureMetric(name string, value float64) {
	if collectionFrameStore == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeMetric,
		metric: &MetricRecord{
			Name:       name,
			Value:      value,
			RecordedAt: time.Now(),
		},
	}
}

func CaptureMetricWithTags(name string, value float64, tags map[string]string) {
	if collectionFrameStore == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeMetric,
		metric: &MetricRecord{
			Name:       name,
			Value:      value,
			RecordedAt: time.Now(),
			Tags:       tags,
		},
	}
}

func CaptureTrace(
	tc *TraceContext,
	endpoint string,
	d time.Duration,
	startedAt time.Time,
	statusCode, bodySize int,
	clientIP string,
) {
	if collectionFrameStore == nil {
		return
	}
	CaptureTraceWithAttributes(tc, endpoint, d, startedAt, statusCode, bodySize, clientIP, nil)
}

func CaptureTraceWithAttributes(
	tc *TraceContext,
	endpoint string,
	d time.Duration,
	startedAt time.Time,
	statusCode, bodySize int,
	clientIP string,
	attributes map[string]string,
) {
	if collectionFrameStore == nil {
		return
	}
	if tc == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeTrace,
		trace: &Trace{
			Id:                 tc.Id,
			Endpoint:           endpoint,
			Duration:           d,
			RecordedAt:         startedAt,
			StatusCode:         statusCode,
			BodySize:           bodySize,
			ClientIP:           clientIP,
			Attributes:         attributes,
			Spans:              tc.GetSpans(),
			DistributedTraceId: tc.DistributedTraceId,
		},
	}
}

func CaptureTask(
	tc *TraceContext,
	taskName string,
	d time.Duration,
	startedAt time.Time,
	attributes map[string]string,
) {
	if collectionFrameStore == nil {
		return
	}
	if tc == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeTrace,
		trace: &Trace{
			Id:                 tc.Id,
			Endpoint:           taskName,
			Duration:           d,
			RecordedAt:         startedAt,
			StatusCode:         0,
			BodySize:           0,
			ClientIP:           "",
			Attributes:         attributes,
			Spans:              tc.GetSpans(),
			IsTask:             true,
			DistributedTraceId: tc.DistributedTraceId,
		},
	}
}

func CaptureTaskException(traceId string, stacktrace string) {
	if collectionFrameStore == nil {
		return
	}
	CaptureTaskExceptionWithAttributes(traceId, stacktrace, nil)
}

func CaptureTaskExceptionWithAttributes(traceId string, stacktrace string, attributes map[string]string) {
	if collectionFrameStore == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeException,
		exceptionStackTrace: &ExceptionStackTrace{
			TraceId:    &traceId,
			IsTask:     true,
			StackTrace: stacktrace,
			RecordedAt: time.Now(),
			Attributes: attributes,
		},
	}
}

func StartSpanByTraceId(ctx context.Context, name string) *ActiveSpan {
	tc := GetTraceFromContext(ctx)
	if tc == nil {
		return nil
	}
	now := time.Now()
	return &ActiveSpan{
		span: Span{
			Id:        uuid.NewString(),
			Name:      name,
			StartTime: now,
		},
		trace:     tc,
		startedAt: now,
	}
}

func StartSpan(ctx context.Context, name string) *ActiveSpan {
	tc := GetTraceFromContext(ctx)
	if tc == nil {
		return nil
	}
	now := time.Now()
	return &ActiveSpan{
		span: Span{
			Id:        uuid.NewString(),
			Name:      name,
			StartTime: now,
		},
		trace:     tc,
		startedAt: now,
	}
}

func CaptureTraceException(traceId string, stacktrace string) {
	if collectionFrameStore == nil {
		return
	}
	CaptureTraceExceptionWithAttributes(traceId, stacktrace, nil)
}

func CaptureTraceExceptionWithAttributes(traceId string, stacktrace string, attributes map[string]string) {
	if collectionFrameStore == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeException,
		exceptionStackTrace: &ExceptionStackTrace{
			TraceId:    &traceId,
			StackTrace: stacktrace,
			RecordedAt: time.Now(),
			Attributes: attributes,
		},
	}
}

func CaptureException(err error) {
	if collectionFrameStore == nil {
		return
	}
	CaptureExceptionWithAttributes(err, nil, nil)
}

func CaptureExceptionWithAttributes(err error, attributes map[string]string, traceId *string) {
	if collectionFrameStore == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeException,
		exceptionStackTrace: &ExceptionStackTrace{
			TraceId:    traceId,
			StackTrace: FormatErrorWithStack(err, CaptureStack(2)),
			RecordedAt: time.Now(),
			Attributes: attributes,
		},
	}
}

func CaptureExceptionWithContext(ctx context.Context, err error) {
	if collectionFrameStore == nil {
		return
	}
	attributes := GetAttributesFromContext(ctx)
	isTask := GetIsTaskFromContext(ctx)
	var distributedTraceId *string
	if tc := GetTraceFromContext(ctx); tc != nil && tc.DistributedTraceId != "" {
		distributedTraceId = &tc.DistributedTraceId
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeException,
		exceptionStackTrace: &ExceptionStackTrace{
			TraceId:            GetTraceIdFromContext(ctx),
			IsTask:             isTask,
			StackTrace:         FormatErrorWithStack(err, CaptureStack(2)),
			RecordedAt:         time.Now(),
			Attributes:         attributes.GetTags(),
			DistributedTraceId: distributedTraceId,
		},
	}
}

func Recover() {
	r := recover()

	if r != nil {
		if collectionFrameStore == nil {
			return
		}
		collectionFrameStore.messageQueue <- CollectionFrameMessage{
			msgType: CollectionFrameMessageTypeException,
			exceptionStackTrace: &ExceptionStackTrace{
				TraceId:    nil,
				StackTrace: FormatRWithStack(r, CaptureStack(2)),
				RecordedAt: time.Now(),
			},
		}
	}
}

func RecoverWithContext(ctx context.Context) {
	r := recover()

	if r != nil {
		if collectionFrameStore == nil {
			return
		}
		attributes := GetAttributesFromContext(ctx)
		collectionFrameStore.messageQueue <- CollectionFrameMessage{
			msgType: CollectionFrameMessageTypeException,
			exceptionStackTrace: &ExceptionStackTrace{
				TraceId:    nil,
				StackTrace: FormatRWithStack(r, CaptureStack(2)),
				RecordedAt: time.Now(),
				Attributes: attributes.GetTags(),
			},
		}
	}
}

func CaptureMessage(msg string) {
	if collectionFrameStore == nil {
		return
	}
	CaptureMessageWithContext(context.Background(), msg)
}

func CaptureMessageWithContext(ctx context.Context, msg string) {
	if collectionFrameStore == nil {
		return
	}

	attributes := GetAttributesFromContext(ctx)
	isTask := GetIsTaskFromContext(ctx)
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeException,
		exceptionStackTrace: &ExceptionStackTrace{
			TraceId:    GetTraceIdFromContext(ctx),
			IsTask:     isTask,
			StackTrace: msg,
			RecordedAt: time.Now(),
			Attributes: attributes.GetTags(),
			IsMessage:  true,
		},
	}
}

func CaptureMessageAttributes(msg string, attributes map[string]string) {
	if collectionFrameStore == nil {
		return
	}
	collectionFrameStore.messageQueue <- CollectionFrameMessage{
		msgType: CollectionFrameMessageTypeException,
		exceptionStackTrace: &ExceptionStackTrace{
			TraceId:    nil,
			StackTrace: msg,
			RecordedAt: time.Now(),
			Attributes: attributes,
			IsMessage:  true,
		},
	}
}

type PanicError struct {
	Value interface{}
	Stack string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("%v\n%s", e.Value, e.Stack)
}

type StackTraceError struct {
	Err    error
	Frames []runtime.Frame
}

func (e *StackTraceError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return ""
}

func (e *StackTraceError) Unwrap() error {
	return e.Err
}

func (e *StackTraceError) GetStackFrames() []runtime.Frame {
	return e.Frames
}

func NewStackTraceErrorf(format string, args ...any) *StackTraceError {
	err := fmt.Errorf(format, args...)

	var existing *StackTraceError
	if errors.As(err, &existing) {
		return &StackTraceError{
			Err:    err,
			Frames: existing.Frames,
		}
	}

	return &StackTraceError{
		Err:    err,
		Frames: CaptureStack(1),
	}
}

func WrapWithStackTrace(err error, skip int) *StackTraceError {
	return &StackTraceError{
		Err:    err,
		Frames: CaptureStack(skip + 1),
	}
}

func wrapAndExecute(ctx context.Context, f TaskExecutor) (s *string, err error) {
	defer func() {
		if r := recover(); r != nil {
			m := FormatRWithStack(r, CaptureStack(2))
			s = &m

			var errFromRecover error
			switch v := r.(type) {
			case error:
				errFromRecover = v
			default:
				errFromRecover = fmt.Errorf("%v", v)
			}

			err = &PanicError{Value: errFromRecover, Stack: m}
		}
	}()

	f(ctx)

	return nil, nil
}

type TaskExecutor = func(ctx context.Context)

func MeasureTask(title string, f TaskExecutor) {
	tc := &TraceContext{
		Id:     uuid.NewString(),
		IsTask: true,
	}
	attributes := NewAttributes()
	start := time.Now()

	ctx := context.WithValue(context.Background(), string(CtxAttributesKey), attributes)
	ctx = context.WithValue(ctx, string(CtxTraceKey), tc)

	stackTraceFormatted, err := wrapAndExecute(ctx, f)

	isError := stackTraceFormatted != nil
	if ShouldSample(isError) {
		duration := time.Since(start)
		CaptureTask(tc, title, duration, start, attributes.GetTags())

		if stackTraceFormatted != nil {
			CaptureTaskExceptionWithAttributes(tc.Id, *stackTraceFormatted, attributes.GetTags())
		}
	}

	if err != nil {
		panic(err)
	}
}
