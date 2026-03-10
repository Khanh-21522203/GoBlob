package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func normalizeUploadOption(opt *UploadOption) *UploadOption {
	if opt == nil {
		return &UploadOption{Count: 1}
	}
	out := *opt
	if out.Count == 0 {
		out.Count = 1
	}
	return &out
}

// Assign requests file IDs from master /dir/assign.
func Assign(ctx context.Context, masterAddr string, opt *UploadOption) (*AssignedFileId, error) {
	normalized := normalizeUploadOption(opt)
	if masterAddr == "" {
		masterAddr = normalized.Master
	}
	if masterAddr == "" {
		return nil, fmt.Errorf("master address is required")
	}

	params := url.Values{}
	if normalized.Collection != "" {
		params.Set("collection", normalized.Collection)
	}
	if normalized.Replication != "" {
		params.Set("replication", normalized.Replication)
	}
	if normalized.Ttl != "" {
		params.Set("ttl", normalized.Ttl)
	}
	if normalized.Count > 0 {
		params.Set("count", strconv.FormatUint(normalized.Count, 10))
	}
	if normalized.DataCenter != "" {
		params.Set("dataCenter", normalized.DataCenter)
	}
	if normalized.Rack != "" {
		params.Set("rack", normalized.Rack)
	}
	if normalized.DiskType != "" {
		params.Set("diskType", normalized.DiskType)
	}
	if normalized.Preallocate > 0 {
		params.Set("preallocate", strconv.FormatInt(normalized.Preallocate, 10))
	}

	assignURL := ensureHTTPPrefix(masterAddr) + "/dir/assign"
	if len(params) > 0 {
		assignURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, assignURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build assign request: %w", err)
	}
	resp, err := defaultHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("assign request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, ErrNoWritableVolumes
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, &HTTPStatusError{
			Op:         "assign",
			URL:        assignURL,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	var result AssignedFileId
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode assign response: %w", err)
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	return &result, nil
}
