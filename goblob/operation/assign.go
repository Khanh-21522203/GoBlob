package operation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type AssignOption struct {
	Collection  string
	Replication string
	Ttl         string
	Count       uint64
	DataCenter  string
	Rack        string
	DiskType    string
}

type AssignedFileId struct {
	Fid       string `json:"fid"`
	Url       string `json:"url"`
	PublicUrl string `json:"publicUrl"`
	Count     uint64 `json:"count"`
	Auth      string `json:"auth"`
}

func Assign(ctx context.Context, masterAddr string, opt *AssignOption) (*AssignedFileId, error) {
	params := url.Values{}
	if opt != nil {
		if opt.Collection != "" {
			params.Set("collection", opt.Collection)
		}
		if opt.Replication != "" {
			params.Set("replication", opt.Replication)
		}
		if opt.Ttl != "" {
			params.Set("ttl", opt.Ttl)
		}
		if opt.Count > 0 {
			params.Set("count", strconv.FormatUint(opt.Count, 10))
		}
		if opt.DataCenter != "" {
			params.Set("dataCenter", opt.DataCenter)
		}
		if opt.Rack != "" {
			params.Set("rack", opt.Rack)
		}
		if opt.DiskType != "" {
			params.Set("diskType", opt.DiskType)
		}
	}

	assignURL := "http://" + masterAddr + "/dir/assign"
	if len(params) > 0 {
		assignURL += "?" + params.Encode()
	}

	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, assignURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, ErrNoWritableVolumes
	}

	var result AssignedFileId
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
