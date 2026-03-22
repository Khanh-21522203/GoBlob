package command

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VolumeTierUploadCommand uploads local tier data to an S3-compatible endpoint.
// For -cloud s3: plain HTTP PUT to -s3-endpoint (works with GoBlob S3 API, MinIO,
// Cloudflare R2, or any S3-compatible server without request signing).
// For -cloud azure|gcs: not yet implemented (returns a clear error).
type VolumeTierUploadCommand struct {
	sourceDir   string
	cloud       string
	bucket      string
	s3Endpoint  string
	apply       bool
	concurrency int
}

func init() {
	Register(&VolumeTierUploadCommand{})
}

func (c *VolumeTierUploadCommand) Name() string { return "volume.tier.upload" }
func (c *VolumeTierUploadCommand) Synopsis() string {
	return "scan and upload local tier data to remote S3-compatible storage"
}
func (c *VolumeTierUploadCommand) Usage() string {
	return `blob volume.tier.upload -source.dir /data/hdd -cloud s3 -bucket archive -s3-endpoint http://localhost:8333 -apply`
}

func (c *VolumeTierUploadCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.sourceDir, "source.dir", "", "source directory containing chunk files")
	fs.StringVar(&c.cloud, "cloud", "s3", "remote storage provider: s3|azure|gcs")
	fs.StringVar(&c.bucket, "bucket", "", "target bucket/container")
	fs.StringVar(&c.s3Endpoint, "s3-endpoint", "", "S3-compatible endpoint URL (e.g. http://localhost:8333)")
	fs.BoolVar(&c.apply, "apply", false, "execute upload; without this flag only a dry-run plan is printed")
	fs.IntVar(&c.concurrency, "c", 4, "number of concurrent upload workers")
}

func (c *VolumeTierUploadCommand) Run(ctx context.Context, _ []string) error {
	if strings.TrimSpace(c.sourceDir) == "" {
		return fmt.Errorf("-source.dir is required")
	}
	if strings.TrimSpace(c.bucket) == "" {
		return fmt.Errorf("-bucket is required")
	}
	cloud := strings.ToLower(strings.TrimSpace(c.cloud))
	switch cloud {
	case "s3":
	case "azure", "gcs":
		return fmt.Errorf("-%s cloud upload is not yet implemented; use -cloud s3 -s3-endpoint <url> to target an S3-compatible endpoint", cloud)
	default:
		return fmt.Errorf("unsupported -cloud value %q; choose s3|azure|gcs", c.cloud)
	}

	info, err := os.Stat(c.sourceDir)
	if err != nil {
		return fmt.Errorf("stat source dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source path is not a directory: %s", c.sourceDir)
	}

	type fileItem struct {
		absPath string
		relPath string
		size    int64
	}
	var files []fileItem
	var totalBytes int64
	err = filepath.WalkDir(c.sourceDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(c.sourceDir, p)
		if err != nil {
			return err
		}
		files = append(files, fileItem{absPath: p, relPath: rel, size: fi.Size()})
		totalBytes += fi.Size()
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan source dir: %w", err)
	}

	endpoint := strings.TrimRight(strings.TrimSpace(c.s3Endpoint), "/")

	fmt.Printf("Tier upload plan\n")
	fmt.Printf("- source:   %s\n", c.sourceDir)
	fmt.Printf("- cloud:    %s\n", cloud)
	fmt.Printf("- bucket:   %s\n", c.bucket)
	fmt.Printf("- endpoint: %s\n", endpoint)
	fmt.Printf("- files:    %d\n", len(files))
	fmt.Printf("- bytes:    %d\n", totalBytes)

	if !c.apply {
		fmt.Println("Dry-run only. Add -apply to execute the upload.")
		return nil
	}
	if endpoint == "" {
		return fmt.Errorf("-s3-endpoint is required when -apply is set")
	}

	if c.concurrency <= 0 {
		c.concurrency = 1
	}
	client := &http.Client{Timeout: 5 * time.Minute}

	type result struct {
		relPath string
		err     error
	}
	jobs := make(chan fileItem, len(files))
	results := make(chan result, len(files))

	for i := 0; i < c.concurrency; i++ {
		go func() {
			for item := range jobs {
				key := strings.ReplaceAll(item.relPath, string(filepath.Separator), "/")
				url := fmt.Sprintf("%s/%s/%s", endpoint, c.bucket, key)
				payload, err := os.ReadFile(item.absPath)
				if err != nil {
					results <- result{item.relPath, err}
					continue
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
				if err != nil {
					results <- result{item.relPath, err}
					continue
				}
				req.ContentLength = int64(len(payload))
				resp, err := client.Do(req)
				if err != nil {
					results <- result{item.relPath, err}
					continue
				}
				_ = resp.Body.Close()
				if resp.StatusCode/100 != 2 {
					results <- result{item.relPath, fmt.Errorf("HTTP %d", resp.StatusCode)}
					continue
				}
				results <- result{item.relPath, nil}
			}
		}()
	}

	for _, f := range files {
		jobs <- f
	}
	close(jobs)

	var okCount, failCount int
	for range files {
		r := <-results
		if r.err != nil {
			fmt.Printf("  FAIL %s: %v\n", r.relPath, r.err)
			failCount++
		} else {
			okCount++
		}
	}
	fmt.Printf("Tier upload done: ok=%d fail=%d\n", okCount, failCount)
	if failCount > 0 {
		return fmt.Errorf("%d file(s) failed to upload", failCount)
	}
	return nil
}
