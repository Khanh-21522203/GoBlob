package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Upload sends data to a volume server.
func Upload(ctx context.Context, uploadURL, filename string, data io.Reader, size int64, isGzip bool, mimeType string, pairMap map[string]string, jwt string) (*UploadResult, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	fieldName := "file"
	if filename == "" {
		filename = "blob.bin"
	}
	fileWriter, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		return nil, fmt.Errorf("create multipart form file: %w", err)
	}
	if _, err := io.Copy(fileWriter, data); err != nil {
		return nil, fmt.Errorf("copy upload payload: %w", err)
	}
	for k, v := range pairMap {
		if err := writer.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("write form field %s: %w", k, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, ensureHTTPPrefix(uploadURL), &buf)
	if err != nil {
		return nil, fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if mimeType != "" {
		req.Header.Set("X-Upload-Content-Type", mimeType)
	}
	if isGzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	if size >= 0 {
		req.ContentLength = int64(buf.Len())
	}

	resp, err := defaultHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, &HTTPStatusError{
			Op:         "upload",
			URL:        uploadURL,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	if result.Fid == "" {
		result.Fid = parseFidFromUploadURL(uploadURL)
	}
	return &result, nil
}

func parseFidFromUploadURL(uploadURL string) string {
	u, err := url.Parse(ensureHTTPPrefix(uploadURL))
	if err != nil {
		return ""
	}
	last := path.Base(strings.TrimSuffix(u.Path, "/"))
	if last == "." || last == "/" {
		return ""
	}
	return last
}
