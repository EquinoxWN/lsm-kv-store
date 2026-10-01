package lsmkv

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func mustOpen(t *testing.T, dir string, opts Options) *DB {
	t.Helper()
	db, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustGet(t *testing.T, db *DB, key, want string) {
	t.Helper()
	got, err := db.Get([]byte(key))
	if err != nil || string(got) != want {
		t.Fatalf("Get(%q) = %q, %v; want %q", key, got, err, want)
	}
}

func mustMiss(t *testing.T, db *DB, key string) {
	t.Helper()
	if _, err := db.Get([]byte(key)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(%q) err = %v; want ErrNotFound", key, err)
	}
}

func TestPutGetDelete(t *testing.T) {
	db := mustOpen(t, t.TempDir(), Options{})
	defer db.Close()

	if err := db.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, db, "a", "1")
	if err := db.Put([]byte("a"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, db, "a", "2")
	if err := db.Delete([]byte("a")); err != nil {
		t.Fatal(err)
	}
	mustMiss(t, db, "a")
	mustMiss(t, db, "never-written")
}

func TestRejectsBadInput(t *testing.T) {
	db := mustOpen(t, t.TempDir(), Options{})
	defer db.Close()
	if err := db.Put(nil, []byte("v")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if err := db.Put([]byte("k"), make([]byte, maxEntry)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestFlushesToTablesAndReopens(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{MemtableSize: 8 << 10, BlockSize: 512, NoSync: true})
	const n = 3000
	for i := 0; i < n; i++ {
		if err := db.Put([]byte(fmt.Sprintf("key-%05d", i)), []byte(fmt.Sprintf("val-%05d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i += 3 {
		if err := db.Delete([]byte(fmt.Sprintf("key-%05d", i))); err != nil {
			t.Fatal(err)
		}
	}
	check := func(db *DB) {
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("key-%05d", i)
			if i%3 == 0 {
				mustMiss(t, db, key)
			} else {
				mustGet(t, db, key, fmt.Sprintf("val-%05d", i))
			}
		}
	}
	check(db)
	if db.Stats().Tables == 0 {
		t.Fatal("expected the small memtable to flush to at least one table")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, dir, Options{MemtableSize: 8 << 10})
	defer db.Close()
	check(db)
	if got := db.Stats().LastSeq; got != n+n/3 {
		t.Fatalf("LastSeq = %d, want %d", got, n+n/3)
	}
}

func TestRecoversUnflushedWritesFromLog(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	for i := 0; i < 100; i++ {
		if err := db.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if db.Stats().Tables != 0 {
		t.Fatal("nothing should be flushed yet")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, dir, Options{})
	defer db.Close()
	for i := 0; i < 100; i++ {
		mustGet(t, db, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	if db.Stats().Tables != 1 {
		t.Fatalf("recovery should flush the replayed log into one table, got %d", db.Stats().Tables)
	}
}

// copyDir snapshots dir byte for byte, as a crash would leave it.
func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		in, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(filepath.Join(dst, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		in.Close()
		out.Close()
	}
	return dst
}

func TestAcknowledgedWritesSurviveCrash(t *testing.T) {
	src := t.TempDir()
	db := mustOpen(t, src, Options{})
	defer db.Close()
	for i := 0; i < 200; i++ {
		if err := db.Put([]byte(fmt.Sprintf("k%03d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	// Snapshot the files without Close: the process "dies" right after the acks.
	crashed := copyDir(t, src)

	// Simulate a torn final record from a write that was never acknowledged.
	logs, _ := filepath.Glob(filepath.Join(crashed, "*.log"))
	if len(logs) != 1 {
		t.Fatalf("want one log, got %v", logs)
	}
	f, err := os.OpenFile(logs[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0xde, 0xad, 0xbe})
	f.Close()

	recovered := mustOpen(t, crashed, Options{})
	defer recovered.Close()
	for i := 0; i < 200; i++ {
		mustGet(t, recovered, fmt.Sprintf("k%03d", i), "v")
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	db := mustOpen(t, t.TempDir(), Options{MemtableSize: 16 << 10, NoSync: true})
	defer db.Close()
	const writers, perWriter = 8, 500
	var wg sync.WaitGroup
	errs := make(chan error, writers*2)
	for w := 0; w < writers; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if err := db.Put([]byte(fmt.Sprintf("w%d-%04d", w, i)), []byte(fmt.Sprint(i))); err != nil {
					errs <- err
					return
				}
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := db.Get([]byte(fmt.Sprintf("w%d-%04d", w, i))); err != nil && !errors.Is(err, ErrNotFound) {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for w := 0; w < writers; w++ {
		for i := 0; i < perWriter; i++ {
			mustGet(t, db, fmt.Sprintf("w%d-%04d", w, i), fmt.Sprint(i))
		}
	}
}

func TestClosedDBRejectsCalls(t *testing.T) {
	db := mustOpen(t, t.TempDir(), Options{})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal("second Close must be a no-op")
	}
	if err := db.Put([]byte("k"), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Put after Close: %v", err)
	}
	if _, err := db.Get([]byte("k")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close: %v", err)
	}
}
