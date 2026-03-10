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
	"strings"

	"GoBlob/goblob/core/types"
)

type VolumeLocation struct {
	Url       string `json:"url"`
	PublicUrl string `json:"publicUrl"`
}

type LookupResult struct {
	VolumeOrFileId string           `json:"volumeOrFileId,omitempty"`
	VolumeId       string           `json:"volumeId,omitempty"`
	Locations      []VolumeLocation `json:"locations"`
	Error          string           `json:"error,omitempty"`
}

// LookupVolumeId queries master and optionally uses a local cache.
func LookupVolumeId(ctx context.Context, masterAddr string, vid types.VolumeId, cache ...*VolumeLocationCache) ([]VolumeLocation, error) {
	var c *VolumeLocationCache
	if len(cache) > 0 {
		c = cache[0]
	}
	if c != nil {
		if locs, ok := c.Get(vid); ok {
			return locs, nil
		}
	}

	base := ensureHTTPPrefix(masterAddr)
	lookupURL := base + "/dir/lookup?" + url.Values{"volumeId": []string{strconv.FormatUint(uint64(vid), 10)}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookupURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build lookup request: %w", err)
	}
	resp, err := defaultHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, &HTTPStatusError{
			Op:         "lookup",
			URL:        lookupURL,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	var result LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode lookup response: %w", err)
	}
	if result.Error != "" {
		if strings.Contains(strings.ToLower(result.Error), "not found") {
			if c != nil {
				c.Invalidate(vid)
			}
			return nil, ErrVolumeNotFound
		}
		return nil, errors.New(result.Error)
	}
	if len(result.Locations) == 0 {
		if c != nil {
			c.Invalidate(vid)
		}
		return nil, ErrVolumeNotFound
	}

	if c != nil {
		c.Set(vid, result.Locations)
	}
	return result.Locations, nil
}
