package operation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"GoBlob/goblob/core/types"
)

type VolumeLocation struct {
	Url       string `json:"url"`
	PublicUrl string `json:"publicUrl"`
}

type LookupResult struct {
	VolumeOrFileId string           `json:"volumeOrFileId"`
	Locations      []VolumeLocation `json:"locations"`
	Error          string           `json:"error"`
}

func LookupVolumeId(ctx context.Context, masterAddr string, vid types.VolumeId) ([]VolumeLocation, error) {
	lookupURL := fmt.Sprintf("http://%s/dir/lookup?volumeId=%d", masterAddr, vid)

	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookupURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}

	return result.Locations, nil
}
