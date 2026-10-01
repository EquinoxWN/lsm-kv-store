package sstable

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

// buildTable writes k0000..k(n-1); every tenth key also gets a later tombstone.
func buildTable(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "000001.sst")
	w, err := Create(path, WriterOptions{BlockSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		user := []byte(fmt.Sprintf("k%04d", i))
		if i%10 == 0 {
			if err := w.Add(ikey.Key{User: user, Seq: uint64(n + i + 1), Kind: ikey.KindDelete}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Add(ikey.Key{User: user, Seq: uint64(i + 1), Kind: ikey.KindSet}, []byte(fmt.Sprintf("v%04d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWriteThenRead(t *testing.T) {
	const n = 1000
	r, err := Open(buildTable(t, n))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.index) < 10 {
		t.Fatalf("expected many blocks with a small block size, got %d", len(r.index))
	}
	for i := 0; i < n; i++ {
		user := []byte(fmt.Sprintf("k%04d", i))
		v, kind, ok, err := r.Get(user, ikey.MaxSeq)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", user, ok, err)
		}
		if i%10 == 0 {
			if kind != ikey.KindDelete {
				t.Fatalf("%s: want tombstone", user)
			}
			// Before the delete, the old value is still visible.
			v, kind, ok, _ = r.Get(user, uint64(i+1))
		}
		if !ok || kind != ikey.KindSet || string(v) != fmt.Sprintf("v%04d", i) {
			t.Fatalf("%s: got %q kind=%d", user, v, kind)
		}
	}
	if r.MaxSeq() != uint64(2*n-9) {
		t.Fatalf("maxSeq = %d", r.MaxSeq())
	}
}

func TestMissingKeys(t *testing.T) {
	r, err := Open(buildTable(t, 200))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, k := range []string{"a", "k0050x", "zzz"} {
		if _, _, ok, err := r.Get([]byte(k), ikey.MaxSeq); ok || err != nil {
			t.Fatalf("%s: ok=%v err=%v", k, ok, err)
		}
	}
}

func TestOutOfOrderAddRejected(t *testing.T) {
	w, err := Create(filepath.Join(t.TempDir(), "x.sst"), WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if err := w.Add(ikey.Key{User: []byte("b"), Seq: 1, Kind: ikey.KindSet}, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(ikey.Key{User: []byte("a"), Seq: 2, Kind: ikey.KindSet}, nil); err == nil {
		t.Fatal("out-of-order key must be rejected")
	}
}

func TestCorruptBlockDetected(t *testing.T) {
	path := buildTable(t, 200)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[10] ^= 0xff // inside the first data block
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, _, _, err := r.Get([]byte("k0001"), ikey.MaxSeq); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestTruncatedFileRejected(t *testing.T) {
	path := buildTable(t, 50)
	if err := os.Truncate(path, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}
