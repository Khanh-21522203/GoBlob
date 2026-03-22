package command

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/operation"
)

type BenchmarkCommand struct {
	master      string
	filer       string
	op          string
	requests    int
	concurrency int
	size        int
	timeout     time.Duration
}

func init() {
	Register(&BenchmarkCommand{})
}

func (c *BenchmarkCommand) Name() string     { return "benchmark" }
func (c *BenchmarkCommand) Synopsis() string { return "benchmark the running GoBlob cluster" }
func (c *BenchmarkCommand) Usage() string {
	return `blob benchmark [flags]

Operations (-op):
  assign       POST /dir/assign on master (ID allocation throughput)
  upload       assign + PUT to volume server (end-to-end write latency)
  download     pre-upload N sample files, then GET from volume server (read latency)
  filer-write  POST a file to filer server
  filer-read   pre-write N files to filer, then GET them (filer read latency)

Examples:
  blob benchmark -master 127.0.0.1:9333 -op assign -n 2000 -c 50
  blob benchmark -master 127.0.0.1:9333 -op upload  -n 1000 -c 20 -size 65536
  blob benchmark -master 127.0.0.1:9333 -op download -n 5000 -c 50
  blob benchmark -filer 127.0.0.1:8888  -op filer-write -n 1000 -c 20 -size 4096
  blob benchmark -filer 127.0.0.1:8888  -op filer-read  -n 2000 -c 30
`
}

func (c *BenchmarkCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.master, "master", "127.0.0.1:9333", "master HTTP address (used by assign/upload/download)")
	fs.StringVar(&c.filer, "filer", "127.0.0.1:8888", "filer HTTP address (used by filer-write/filer-read)")
	fs.StringVar(&c.op, "op", "assign", "operation to benchmark: assign|upload|download|filer-write|filer-read")
	fs.IntVar(&c.requests, "n", 1000, "number of requests")
	fs.IntVar(&c.concurrency, "c", 20, "number of concurrent workers")
	fs.IntVar(&c.size, "size", 4096, "payload size in bytes for write operations")
	fs.DurationVar(&c.timeout, "timeout", 10*time.Second, "per-request timeout")
}

// benchResult holds raw latency samples collected during a benchmark run.
type benchResult struct {
	ok        int64
	fail      int64
	latencies []int64 // nanoseconds, one per successful request
}

// stats computes QPS and latency percentiles from collected samples.
func (r *benchResult) stats(elapsed time.Duration) string {
	qps := float64(r.ok) / elapsed.Seconds()

	if len(r.latencies) == 0 {
		return fmt.Sprintf("qps=%.2f  (no successful requests)", qps)
	}

	sorted := make([]int64, len(r.latencies))
	copy(sorted, r.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	pct := func(p float64) string {
		idx := int(float64(len(sorted)-1) * p)
		ms := float64(sorted[idx]) / float64(time.Millisecond)
		return strconv.FormatFloat(ms, 'f', 2, 64) + "ms"
	}

	return fmt.Sprintf("qps=%.2f  p50=%s  p95=%s  p99=%s  p999=%s",
		qps, pct(0.50), pct(0.95), pct(0.99), pct(0.999))
}

func (c *BenchmarkCommand) Run(ctx context.Context, _ []string) error {
	if c.requests <= 0 {
		return fmt.Errorf("-n must be > 0")
	}
	if c.concurrency <= 0 {
		c.concurrency = 1
	}
	if c.size <= 0 {
		c.size = 4096
	}

	client := &http.Client{Timeout: c.timeout}

	switch c.op {
	case "assign":
		return c.runAssign(ctx, client)
	case "upload":
		return c.runUpload(ctx, client)
	case "download":
		return c.runDownload(ctx, client)
	case "filer-write":
		return c.runFilerWrite(ctx, client)
	case "filer-read":
		return c.runFilerRead(ctx, client)
	default:
		return fmt.Errorf("unknown op %q; choose assign|upload|download|filer-write|filer-read", c.op)
	}
}

// runAssign benchmarks POST /dir/assign on the master.
func (c *BenchmarkCommand) runAssign(ctx context.Context, client *http.Client) error {
	master := toHTTPAddr(c.master)
	assignURL := master + "/dir/assign?count=1"

	res, elapsed := c.runWorkers(ctx, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, assignURL, nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode/100 == 2, nil
	})

	c.printResult("assign", map[string]string{"master": c.master}, res, elapsed)
	return nil
}

// runUpload benchmarks end-to-end assign + volume PUT.
func (c *BenchmarkCommand) runUpload(ctx context.Context, _ *http.Client) error {
	payload := make([]byte, c.size)

	res, elapsed := c.runWorkers(ctx, func() (bool, error) {
		assigned, err := operation.Assign(ctx, c.master, nil)
		if err != nil {
			return false, err
		}
		uploadURL := toHTTPAddr(assigned.Url) + "/" + assigned.Fid
		_, err = operation.Upload(ctx, uploadURL, "blob.bin", bytes.NewReader(payload), int64(c.size), false, "application/octet-stream", nil, assigned.Auth)
		return err == nil, err
	})

	c.printResult("upload", map[string]string{
		"master": c.master,
		"size":   fmt.Sprintf("%dB", c.size),
	}, res, elapsed)
	return nil
}

// runDownload pre-uploads sample files then benchmarks volume GET.
func (c *BenchmarkCommand) runDownload(ctx context.Context, client *http.Client) error {
	sampleSize := c.requests
	if sampleSize > 500 {
		sampleSize = 500
	}

	fmt.Printf("pre-uploading %d sample files (%dB each)...\n", sampleSize, c.size)
	fids, urls, err := c.preUpload(ctx, sampleSize)
	if err != nil {
		return fmt.Errorf("pre-upload failed: %w", err)
	}
	fmt.Printf("pre-upload done, starting download benchmark\n")

	res, elapsed := c.runWorkers(ctx, func() (bool, error) {
		i := rand.Intn(len(fids))
		url := toHTTPAddr(urls[i]) + "/" + fids[i]
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK, nil
	})

	c.printResult("download", map[string]string{
		"master":  c.master,
		"samples": strconv.Itoa(sampleSize),
	}, res, elapsed)
	return nil
}

// runFilerWrite benchmarks POST to filer.
func (c *BenchmarkCommand) runFilerWrite(ctx context.Context, client *http.Client) error {
	filerBase := toHTTPAddr(c.filer)
	payload := make([]byte, c.size)
	var counter int64

	res, elapsed := c.runWorkers(ctx, func() (bool, error) {
		n := atomic.AddInt64(&counter, 1)
		url := fmt.Sprintf("%s/bench/file-%d", filerBase, n)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return false, err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = int64(c.size)
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode/100 == 2, nil
	})

	c.printResult("filer-write", map[string]string{
		"filer": c.filer,
		"size":  fmt.Sprintf("%dB", c.size),
	}, res, elapsed)
	return nil
}

// runFilerRead pre-writes files then benchmarks filer GET.
func (c *BenchmarkCommand) runFilerRead(ctx context.Context, client *http.Client) error {
	sampleSize := c.requests
	if sampleSize > 500 {
		sampleSize = 500
	}
	filerBase := toHTTPAddr(c.filer)
	payload := make([]byte, c.size)

	fmt.Printf("pre-writing %d files to filer (%dB each)...\n", sampleSize, c.size)
	paths := make([]string, sampleSize)
	for i := 0; i < sampleSize; i++ {
		paths[i] = fmt.Sprintf("/bench-read/file-%d", i)
		url := filerBase + paths[i]
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("pre-write request build failed: %w", err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = int64(c.size)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("pre-write failed: %w", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("pre-write got HTTP %d", resp.StatusCode)
		}
	}
	fmt.Printf("pre-write done, starting filer-read benchmark\n")

	res, elapsed := c.runWorkers(ctx, func() (bool, error) {
		p := paths[rand.Intn(len(paths))]
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, filerBase+p, nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK, nil
	})

	c.printResult("filer-read", map[string]string{
		"filer":   c.filer,
		"samples": strconv.Itoa(sampleSize),
	}, res, elapsed)
	return nil
}

// runWorkers executes fn across c.concurrency goroutines for c.requests total,
// collecting per-request latencies. Returns a benchResult and total elapsed time.
func (c *BenchmarkCommand) runWorkers(ctx context.Context, fn func() (bool, error)) (*benchResult, time.Duration) {
	type sample struct {
		ok      bool
		latency int64
	}

	samples := make(chan sample, c.requests)
	var nextIdx int64
	var wg sync.WaitGroup

	start := time.Now()
	wg.Add(c.concurrency)
	for i := 0; i < c.concurrency; i++ {
		go func() {
			defer wg.Done()
			for {
				n := atomic.AddInt64(&nextIdx, 1)
				if int(n) > c.requests {
					return
				}
				t0 := time.Now()
				ok, _ := fn()
				lat := time.Since(t0).Nanoseconds()
				samples <- sample{ok: ok, latency: lat}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(samples)

	res := &benchResult{latencies: make([]int64, 0, c.requests)}
	for s := range samples {
		if s.ok {
			res.ok++
			res.latencies = append(res.latencies, s.latency)
		} else {
			res.fail++
		}
	}
	return res, elapsed
}

// preUpload uploads sampleSize blobs and returns their fids and volume urls.
func (c *BenchmarkCommand) preUpload(ctx context.Context, sampleSize int) (fids, urls []string, err error) {
	payload := make([]byte, c.size)
	fids = make([]string, 0, sampleSize)
	urls = make([]string, 0, sampleSize)

	for i := 0; i < sampleSize; i++ {
		assigned, err := operation.Assign(ctx, c.master, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("assign: %w", err)
		}
		uploadURL := toHTTPAddr(assigned.Url) + "/" + assigned.Fid
		_, err = operation.Upload(ctx, uploadURL, "blob.bin", bytes.NewReader(payload), int64(c.size), false, "application/octet-stream", nil, assigned.Auth)
		if err != nil {
			return nil, nil, fmt.Errorf("upload: %w", err)
		}
		fids = append(fids, assigned.Fid)
		urls = append(urls, assigned.Url)
	}
	return fids, urls, nil
}

// printResult prints benchmark results in a consistent two-line format.
func (c *BenchmarkCommand) printResult(op string, labels map[string]string, res *benchResult, elapsed time.Duration) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("benchmark.%s", op))
	for k, v := range labels {
		sb.WriteString(fmt.Sprintf(" %s=%s", k, v))
	}
	sb.WriteString(fmt.Sprintf(" n=%d c=%d ok=%d fail=%d elapsed=%s",
		c.requests, c.concurrency, res.ok, res.fail, elapsed.Round(time.Millisecond)))
	fmt.Println(sb.String())
	fmt.Printf("  %s\n", res.stats(elapsed))
}

func toHTTPAddr(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + string(types.ServerAddress(addr).ToHttpAddress())
}
