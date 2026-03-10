package command

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"GoBlob/goblob/core/types"
)

type BenchmarkCommand struct {
	master      string
	requests    int
	concurrency int
	timeout     time.Duration
}

func init() {
	Register(&BenchmarkCommand{})
}

func (c *BenchmarkCommand) Name() string     { return "benchmark" }
func (c *BenchmarkCommand) Synopsis() string { return "run lightweight master assign benchmark" }
func (c *BenchmarkCommand) Usage() string {
	return "blob benchmark -master 127.0.0.1:9333 -n 1000 -c 20"
}

func (c *BenchmarkCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.master, "master", "127.0.0.1:9333", "master HTTP address")
	fs.IntVar(&c.requests, "n", 1000, "number of assign requests")
	fs.IntVar(&c.concurrency, "c", 20, "number of concurrent workers")
	fs.DurationVar(&c.timeout, "timeout", 5*time.Second, "request timeout")
}

func (c *BenchmarkCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	if c.requests <= 0 {
		return fmt.Errorf("n must be > 0")
	}
	if c.concurrency <= 0 {
		c.concurrency = 1
	}
	master := strings.TrimSpace(c.master)
	if master == "" {
		return fmt.Errorf("empty master address")
	}
	master = string(types.ServerAddress(master).ToHttpAddress())
	assignURL := fmt.Sprintf("http://%s/dir/assign?count=1", master)
	client := &http.Client{Timeout: c.timeout}

	var (
		okCount   int64
		failCount int64
		nextIdx   int64
		wg        sync.WaitGroup
	)
	start := time.Now()

	worker := func() {
		defer wg.Done()
		for {
			n := atomic.AddInt64(&nextIdx, 1)
			if int(n) > c.requests {
				return
			}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, assignURL, nil)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				atomic.AddInt64(&okCount, 1)
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}
	}

	wg.Add(c.concurrency)
	for i := 0; i < c.concurrency; i++ {
		go worker()
	}
	wg.Wait()
	elapsed := time.Since(start)
	qps := float64(okCount) / elapsed.Seconds()

	fmt.Printf("benchmark.assign master=%s n=%d c=%d ok=%d fail=%d elapsed=%s qps=%s\n",
		master,
		c.requests,
		c.concurrency,
		okCount,
		failCount,
		elapsed.Round(time.Millisecond),
		strconv.FormatFloat(qps, 'f', 2, 64),
	)
	return nil
}
