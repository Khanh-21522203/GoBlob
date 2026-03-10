package operation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPTimeout    = 30 * time.Second
	defaultChunkSizeBytes = int64(8 * 1024 * 1024)
	defaultChunkWorkers   = 3
	maxRetryBackoff       = 30 * time.Second
	initialRetryBackoff   = 500 * time.Millisecond
)

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPTimeout}
}

func ensureHTTPPrefix(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if ok := errors.As(err, &netErr); ok {
		return true
	}
	var httpErr *HTTPStatusError
	if ok := errors.As(err, &httpErr); ok {
		switch httpErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

// UploadWithRetry retries Upload with exponential backoff.
func UploadWithRetry(ctx context.Context, uploadURL, filename string, data []byte, mimeType string, jwt string, maxRetries int) (*UploadResult, error) {
	if maxRetries < 0 {
		maxRetries = 0
	}
	backoff := initialRetryBackoff
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := Upload(ctx, uploadURL, filename, bytes.NewReader(data), int64(len(data)), false, mimeType, nil, jwt)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxRetries {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxRetryBackoff {
			backoff = maxRetryBackoff
		}
	}
	return nil, lastErr
}

// Delete removes a file from a volume server.
func Delete(ctx context.Context, url string, jwt string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, ensureHTTPPrefix(url), nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	resp, err := defaultHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("delete request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return &HTTPStatusError{
			Op:         "delete",
			URL:        url,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}
	return nil
}

type chunkJob struct {
	index  int
	offset int64
	data   []byte
}

type chunkResult struct {
	index int
	item  *ChunkUploadResult
	err   error
}

// ChunkUpload splits a stream into chunks and uploads each chunk in parallel.
func ChunkUpload(ctx context.Context, masterAddr string, reader io.Reader, totalSize int64, chunkSizeBytes int64, opt *UploadOption) ([]*ChunkUploadResult, error) {
	_ = totalSize
	if chunkSizeBytes <= 0 {
		chunkSizeBytes = defaultChunkSizeBytes
	}
	workers := defaultChunkWorkers
	if workers < 1 {
		workers = 1
	}
	uploadOpt := normalizeUploadOption(opt)
	if masterAddr == "" {
		masterAddr = uploadOpt.Master
	}
	if masterAddr == "" {
		return nil, fmt.Errorf("master address is required")
	}

	jobs := make(chan chunkJob, workers)
	results := make(chan chunkResult, workers)
	var wg sync.WaitGroup

	workerFn := func() {
		defer wg.Done()
		for job := range jobs {
			select {
			case <-ctx.Done():
				results <- chunkResult{index: job.index, err: ctx.Err()}
				continue
			default:
			}

			assignRes, err := Assign(ctx, masterAddr, uploadOpt)
			if err != nil {
				results <- chunkResult{index: job.index, err: fmt.Errorf("assign chunk at offset %d: %w", job.offset, err)}
				continue
			}

			uploadURL := ensureHTTPPrefix(assignRes.Url)
			if !strings.HasSuffix(uploadURL, "/") {
				uploadURL += "/"
			}
			uploadURL += assignRes.Fid

			uploadRes, err := UploadWithRetry(ctx, uploadURL, "", job.data, "application/octet-stream", assignRes.Auth, 3)
			if err != nil {
				results <- chunkResult{index: job.index, err: fmt.Errorf("upload chunk at offset %d: %w", job.offset, err)}
				continue
			}

			results <- chunkResult{
				index: job.index,
				item: &ChunkUploadResult{
					FileId:       assignRes.Fid,
					Offset:       job.offset,
					Size:         int64(len(job.data)),
					ModifiedTsNs: time.Now().UnixNano(),
					ETag:         uploadRes.ETag,
				},
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go workerFn()
	}

	chunkCount := 0
	readBuf := make([]byte, chunkSizeBytes)
	offset := int64(0)
	for {
		n, err := io.ReadFull(reader, readBuf)
		if err == io.EOF {
			break
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			close(jobs)
			wg.Wait()
			close(results)
			return nil, fmt.Errorf("read chunk data failed: %w", err)
		}
		if n == 0 {
			break
		}

		data := make([]byte, n)
		copy(data, readBuf[:n])
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(results)
			return nil, ctx.Err()
		case jobs <- chunkJob{index: chunkCount, offset: offset, data: data}:
		}

		offset += int64(n)
		chunkCount++

		if err == io.ErrUnexpectedEOF {
			break
		}
	}

	close(jobs)
	go func() {
		wg.Wait()
		close(results)
	}()

	collected := make([]chunkResult, 0, chunkCount)
	for res := range results {
		if res.err != nil {
			return nil, res.err
		}
		collected = append(collected, res)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].index < collected[j].index })

	out := make([]*ChunkUploadResult, 0, len(collected))
	for _, r := range collected {
		out = append(out, r.item)
	}
	return out, nil
}
