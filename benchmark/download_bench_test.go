package benchmark

import (
	"context"
	"testing"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/memory"
)

func BenchmarkDownload1MB(b *testing.B) {
	ctx := context.Background()
	store := memory.New("bench")
	payload := make([]byte, 1024*1024)
	entry := &filer.Entry{FullPath: "/bench/blob", Content: payload}
	if err := store.InsertEntry(ctx, entry); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := store.FindEntry(ctx, "/bench/blob")
		if err != nil {
			b.Fatal(err)
		}
		if len(got.Content) != len(payload) {
			b.Fatalf("size mismatch %d != %d", len(got.Content), len(payload))
		}
	}
}
