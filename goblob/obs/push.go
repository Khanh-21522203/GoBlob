package obs

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

// PushConfig controls periodic metrics push.
type PushConfig struct {
	URL      string
	Job      string
	Interval time.Duration
}

// StartMetricsPusher starts a background push loop and returns a cancel function.
func StartMetricsPusher(cfg PushConfig) (cancel func()) {
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		return func() {}
	}
	job := strings.TrimSpace(cfg.Job)
	if job == "" {
		job = "goblob"
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}

	logger := New("metrics-push")
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := push.New(url, job).Gatherer(prometheus.DefaultGatherer).Add(); err != nil {
					logger.Warn("pushgateway push failed", slog.String("error", err.Error()))
				}
			}
		}
	}()
	return stop
}
