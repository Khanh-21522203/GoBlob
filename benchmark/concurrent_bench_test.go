package benchmark

import (
	"context"
	"fmt"
	"testing"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/memory"
)

func BenchmarkUploadConcurrent10(b *testing.B) {
	benchmarkUploadConcurrent(b, 10)
}

func BenchmarkUploadConcurrent100(b *testing.B) {
	benchmarkUploadConcurrent(b, 100)
}

func benchmarkUploadConcurrent(b *testing.B, _ int) {
	ctx := context.Background()
	store := memory.New("bench")
	payload := []byte("concurrent")
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = store.InsertEntry(ctx, &filer.Entry{FullPath: filer.FullPath(fmt.Sprintf("/bench/%d", i)), Content: payload})
			i++
		}
	})
}
