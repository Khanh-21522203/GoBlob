package benchmark

import (
	"context"
	"fmt"
	"testing"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/memory"
)

func BenchmarkList1000Entries(b *testing.B) {
	ctx := context.Background()
	store := memory.New("bench")
	for i := 0; i < 1000; i++ {
		_ = store.InsertEntry(ctx, &filer.Entry{FullPath: filer.FullPath(fmt.Sprintf("/bench/file-%04d", i)), Content: []byte("x")})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		_, err := store.ListDirectoryEntries(ctx, "/bench", "", true, 1000, func(e *filer.Entry) bool {
			_ = e
			count++
			return true
		})
		if err != nil {
			b.Fatal(err)
		}
		if count != 1000 {
			b.Fatalf("expected 1000 entries got %d", count)
		}
	}
}
