package system

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type Stats struct {
	CpuPercent    float64
	HasCpuPercent bool
	TotalMemory   uint64
}

type Collector struct {
	reader   *sdkmetric.ManualReader
	provider *sdkmetric.MeterProvider

	mu        sync.Mutex
	lastIdle  float64
	lastTotal float64
	hasLast   bool
}

func NewCollector() (*Collector, error) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if err := host.Start(host.WithMeterProvider(provider)); err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, fmt.Errorf("starting otel host instrumentation: %w", err)
	}
	c := &Collector{reader: reader, provider: provider}
	_, _ = c.Read(context.Background())
	return c, nil
}

func (c *Collector) Shutdown(ctx context.Context) error {
	return c.provider.Shutdown(ctx)
}

var (
	cpuModeKeys  = []string{"cpu.mode", "state"}
	memStateKeys = []string{"system.memory.state", "state"}
)

func (c *Collector) Read(ctx context.Context) (Stats, error) {
	var rm metricdata.ResourceMetrics
	collectErr := c.reader.Collect(ctx, &rm)

	var (
		idle, total  float64
		cpuSeen      bool
		memUsedBytes int64
		memFreeBytes int64
		memUsedUtil  float64
	)

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch m.Name {
			case "system.cpu.time":
				data, ok := m.Data.(metricdata.Sum[float64])
				if !ok {
					continue
				}
				for _, dp := range data.DataPoints {
					mode, ok := attrValue(dp.Attributes, cpuModeKeys)
					if !ok {
						continue
					}
					cpuSeen = true
					total += dp.Value
					if mode == "idle" {
						idle += dp.Value
					}
				}
			case "system.memory.usage":
				data, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					continue
				}
				for _, dp := range data.DataPoints {
					switch state, _ := attrValue(dp.Attributes, memStateKeys); state {
					case "used":
						memUsedBytes = dp.Value
					case "free", "available":
						memFreeBytes = dp.Value
					}
				}
			case "system.memory.utilization":
				data, ok := m.Data.(metricdata.Gauge[float64])
				if !ok {
					continue
				}
				for _, dp := range data.DataPoints {
					if state, _ := attrValue(dp.Attributes, memStateKeys); state == "used" {
						memUsedUtil = dp.Value
					}
				}
			}
		}
	}

	if collectErr != nil && !cpuSeen && memUsedBytes == 0 {
		return Stats{}, collectErr
	}

	var stats Stats

	if memUsedUtil > 0 && memUsedBytes > 0 {
		stats.TotalMemory = uint64(float64(memUsedBytes)/memUsedUtil + 0.5)
	} else if memUsedBytes > 0 && memFreeBytes > 0 {
		stats.TotalMemory = uint64(memUsedBytes) + uint64(memFreeBytes)
	}

	c.mu.Lock()
	if cpuSeen {
		if c.hasLast {
			dTotal := total - c.lastTotal
			dIdle := idle - c.lastIdle
			if dTotal > 0 {
				pct := 100 * (1 - dIdle/dTotal)
				stats.CpuPercent = min(max(pct, 0), 100)
				stats.HasCpuPercent = true
			}
		}
		c.lastIdle, c.lastTotal, c.hasLast = idle, total, true
	}
	c.mu.Unlock()

	return stats, nil
}

func attrValue(set attribute.Set, keys []string) (string, bool) {
	for _, k := range keys {
		if v, ok := set.Value(attribute.Key(k)); ok {
			return v.AsString(), true
		}
	}
	return "", false
}
