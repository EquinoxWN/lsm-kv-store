package lsmkv

import (
	"fmt"
	"testing"
)

func benchPut(b *testing.B, opts Options) {
	db, err := Open(b.TempDir(), opts)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	value := make([]byte, 100)
	b.SetBytes(int64(len(value)) + 16)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Put([]byte(fmt.Sprintf("key-%016d", i)), value); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPutSync measures durable writes: one fsync per Put.
func BenchmarkPutSync(b *testing.B) { benchPut(b, Options{}) }

// BenchmarkPutNoSync measures the in-memory and flush path without fsync.
func BenchmarkPutNoSync(b *testing.B) { benchPut(b, Options{NoSync: true, MemtableSize: 1 << 20}) }

// BenchmarkGet measures point reads spread over memtable and flushed tables.
func BenchmarkGet(b *testing.B) {
	db, err := Open(b.TempDir(), Options{NoSync: true, MemtableSize: 256 << 10})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	const n = 50000
	for i := 0; i < n; i++ {
		if err := db.Put([]byte(fmt.Sprintf("key-%016d", i)), make([]byte, 100)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Get([]byte(fmt.Sprintf("key-%016d", (i*7919)%n))); err != nil {
			b.Fatal(err)
		}
	}
}
