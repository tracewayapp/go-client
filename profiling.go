package traceway

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

const (
	profileIngestPath        = "/api/profiles/ingest"
	defaultProfilingInterval = 60 * time.Second
	defaultCPUProfileWindow  = 30 * time.Second
	profileUploadTimeout     = 30 * time.Second
)

func WithProfiling(serviceName string) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.profilingEnabled = true
		if serviceName == "" {
			serviceName = filepath.Base(os.Args[0])
		}
		s.profilingService = serviceName
	}
}

func WithProfilingInterval(d time.Duration) func(*TracewayOptions) {
	return func(s *TracewayOptions) {
		s.profilingInterval = d
	}
}

func profileIngestURL(reportURL string) (string, error) {
	u, err := url.Parse(reportURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("traceway: cannot derive profile ingest URL from %q", reportURL)
	}
	return u.Scheme + "://" + u.Host + profileIngestPath, nil
}

func captureCPUProfile(d time.Duration) ([]byte, error) {
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		return nil, err
	}
	time.Sleep(d)
	pprof.StopCPUProfile()
	return buf.Bytes(), nil
}

func captureHeapProfile() ([]byte, error) {
	prof := pprof.Lookup("heap")
	if prof == nil {
		return nil, fmt.Errorf("traceway: heap profile unavailable")
	}
	var buf bytes.Buffer
	if err := prof.WriteTo(&buf, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type profiler struct {
	url        string
	token      string
	service    string
	serverName string
	appVersion string
	interval   time.Duration
	cpuWindow  time.Duration
	client     *http.Client
	debug      bool
}

func (p *profiler) uploadProfile(body []byte) error {
	req, err := http.NewRequest(http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	q := req.URL.Query()
	q.Set("service", p.service)
	q.Set("serverName", p.serverName)
	q.Set("appVersion", p.appVersion)
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/octet-stream")

	client := p.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("traceway: profile ingest returned status %d", resp.StatusCode)
	}
	return nil
}

func (p *profiler) collectOnce() {
	window := p.cpuWindow
	if window <= 0 {
		window = defaultCPUProfileWindow
	}

	if cpu, err := captureCPUProfile(window); err != nil {
		p.logError("cpu profile capture failed", err)
	} else if err := p.uploadProfile(cpu); err != nil {
		p.logError("cpu profile upload failed", err)
	}

	if heap, err := captureHeapProfile(); err != nil {
		p.logError("heap profile capture failed", err)
	} else if err := p.uploadProfile(heap); err != nil {
		p.logError("heap profile upload failed", err)
	}
}

func (p *profiler) safeCollectOnce() {
	defer func() {
		if r := recover(); r != nil {
			p.logError("profiling cycle panicked", fmt.Errorf("%v", r))
		}
	}()
	p.collectOnce()
}

func (p *profiler) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for range ticker.C {
		p.safeCollectOnce()
	}
}

func (p *profiler) logError(msg string, err error) {
	if p.debug {
		log.Printf("Traceway: %s: %v", msg, err)
	}
}

func startProfiler(reportURL, token string, opts *TracewayOptions) error {
	ingestURL, err := profileIngestURL(reportURL)
	if err != nil {
		return err
	}

	interval := opts.profilingInterval
	if interval <= 0 {
		interval = defaultProfilingInterval
	}

	cpuWindow := defaultCPUProfileWindow
	if half := interval / 2; half < cpuWindow {
		cpuWindow = half
	}

	p := &profiler{
		url:        ingestURL,
		token:      token,
		service:    opts.profilingService,
		serverName: opts.serverName,
		appVersion: opts.version,
		interval:   interval,
		cpuWindow:  cpuWindow,
		client:     &http.Client{Timeout: profileUploadTimeout},
		debug:      opts.debug,
	}

	go p.run()

	return nil
}
