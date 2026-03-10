package benchmark

import (
	"context"
	"fmt"
	"testing"
	"time"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/memory"
)

func BenchmarkUpload4KB(b *testing.B)  { benchmarkUpload(b, 4*1024) }
func BenchmarkUpload64KB(b *testing.B) { benchmarkUpload(b, 64*1024) }
func BenchmarkUpload1MB(b *testing.B)  { benchmarkUpload(b, 1024*1024) }

func benchmarkUpload(b *testing.B, size int) {
	ctx := context.Background()
	store := memory.New("bench")
	payload := make([]byte, size)
	b.SetBytes(int64(size))
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		entry := &filer.Entry{FullPath: filer.FullPath(fmt.Sprintf("/bench/file-%d", i)), Content: payload}
		if err := store.InsertEntry(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	b.ReportMetric(float64(int64(size)*int64(b.N))/elapsed.Seconds()/1024/1024, "MB/s")
}
