package command

import (
	"context"
	"fmt"
	"time"

	"GoBlob/goblob/obs"
)

type metricsRuntime struct {
	server     *obs.MetricsServer
	cancelPush func()
}

func startMetricsRuntime(host string, port int, pushURL, pushJob string) *metricsRuntime {
	if port <= 0 {
		return &metricsRuntime{cancelPush: func() {}}
	}
	ms := obs.NewMetricsServer(fmt.Sprintf("%s:%d", host, port))
	ms.Start()
	cancel := obs.StartMetricsPusher(obs.PushConfig{URL: pushURL, Job: pushJob, Interval: 15 * time.Second})
	return &metricsRuntime{server: ms, cancelPush: cancel}
}

func (m *metricsRuntime) shutdown(ctx context.Context) {
	if m == nil {
		return
	}
	if m.cancelPush != nil {
		m.cancelPush()
	}
	if m.server != nil {
		_ = m.server.Stop(ctx)
	}
}
