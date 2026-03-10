package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type UploadResult struct {
	Name  string `json:"name"`
	Size  uint32 `json:"size"`
	ETag  string `json:"eTag"`
	Mime  string `json:"mime"`
	Fid   string `json:"fid"`
	Error string `json:"error"`
}

func Upload(ctx context.Context, uploadUrl string, filename string, data io.Reader, size int64, mimeType string, jwt string) (*UploadResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(fw, data); err != nil {
		return nil, err
	}

	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadUrl, &buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", mw.FormDataContentType())

	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}

	return &result, nil
}
