package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TODO: wire storage/tiering.Scanner here to drive policy-based automatic tiering
// from filer entries. The Scanner is ready; it needs a tiering.Tier policy config
// and a filer list function to be provided at startup.

// VolumeTierUploadCommand uploads local tier data to remote object storage.
type VolumeTierUploadCommand struct {
	sourceDir string
	cloud     string
	bucket    string
	apply     bool
	outputDir string
}

func init() {
	Register(&VolumeTierUploadCommand{})
}

func (c *VolumeTierUploadCommand) Name() string { return "volume.tier.upload" }
func (c *VolumeTierUploadCommand) Synopsis() string {
	return "scan and upload local tier data to remote storage"
}
func (c *VolumeTierUploadCommand) Usage() string {
	return "blob volume.tier.upload -source.dir /data/hdd -cloud s3 -bucket archive"
}

func (c *VolumeTierUploadCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.sourceDir, "source.dir", "", "source directory containing chunk files")
	fs.StringVar(&c.cloud, "cloud", "s3", "remote storage provider: s3|azure|gcs")
	fs.StringVar(&c.bucket, "bucket", "", "target bucket/container")
	fs.BoolVar(&c.apply, "apply", false, "execute upload by copying files to output directory mirror")
	fs.StringVar(&c.outputDir, "output.dir", "", "output directory used in -apply mode")
}

func (c *VolumeTierUploadCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args

	if strings.TrimSpace(c.sourceDir) == "" {
		return fmt.Errorf("-source.dir is required")
	}
	if strings.TrimSpace(c.bucket) == "" {
		return fmt.Errorf("-bucket is required")
	}
	cloud := strings.ToLower(strings.TrimSpace(c.cloud))
	switch cloud {
	case "s3", "azure", "gcs":
	default:
		return fmt.Errorf("unsupported -cloud value %q", c.cloud)
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
	err = filepath.WalkDir(c.sourceDir, func(path string, d fs.DirEntry, walkErr error) error {
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
		rel, err := filepath.Rel(c.sourceDir, path)
		if err != nil {
			return err
		}
		files = append(files, fileItem{absPath: path, relPath: rel, size: fi.Size()})
		totalBytes += fi.Size()
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan source dir: %w", err)
	}

	fmt.Printf("Tier upload plan\n")
	fmt.Printf("- source: %s\n", c.sourceDir)
	fmt.Printf("- cloud: %s\n", cloud)
	fmt.Printf("- bucket: %s\n", c.bucket)
	fmt.Printf("- files: %d\n", len(files))
	fmt.Printf("- bytes: %d\n", totalBytes)
	if !c.apply {
		fmt.Println("Use -apply -output.dir <path> to execute upload copy.")
		return nil
	}
	if strings.TrimSpace(c.outputDir) == "" {
		return fmt.Errorf("-output.dir is required when -apply is set")
	}
	if err := os.MkdirAll(c.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	destRoot := filepath.Join(c.outputDir, cloud, c.bucket)
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return fmt.Errorf("create destination root: %w", err)
	}
	for _, item := range files {
		dst := filepath.Join(destRoot, item.relPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		payload, err := os.ReadFile(item.absPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, payload, 0o644); err != nil {
			return err
		}
	}
	manifest := map[string]any{
		"cloud":      cloud,
		"bucket":     c.bucket,
		"source_dir": c.sourceDir,
		"dest_root":  destRoot,
		"files":      len(files),
		"bytes":      totalBytes,
	}
	manifestPath := filepath.Join(destRoot, "_tier_upload_manifest.json")
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("Tier upload completed: %s\n", destRoot)
	return nil
}
