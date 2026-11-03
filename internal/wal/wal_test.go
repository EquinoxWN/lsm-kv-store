package wal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

func writeLog(t *testing.T, n int) (string, []Record) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "000001.log")
	w, err := Create(path, true)
	if err != nil {
		t.Fatal(err)
	}
	var recs []Record
	for i := 0; i < n; i++ {
		r := Record{Kind: ikey.KindSet, Seq: uint64(i + 1), Key: []byte(fmt.Sprintf("k%03d", i)), Value: []byte(fmt.Sprintf("v%03d", i))}
		if i%7 == 0 {
			r.Kind, r.Value = ikey.KindDelete, nil
		}
		if err := w.Append(r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path, recs
}

func collect(t *testing.T, path string) ([]Record, ReplayResult) {
	t.Helper()
	var got []Record
	res, err := Replay(path, func(r Record) error {
		got = append(got, Record{Kind: r.Kind, Seq: r.Seq, Key: bytes.Clone(r.Key), Value: bytes.Clone(r.Value)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, res
}

func sameRecord(a, b Record) bool {
	return a.Kind == b.Kind && a.Seq == b.Seq && bytes.Equal(a.Key, b.Key) && bytes.Equal(a.Value, b.Value)
}

func TestRoundTrip(t *testing.T) {
	path, want := writeLog(t, 50)
	got, res := collect(t, path)
	if res.TornTail || len(got) != len(want) {
		t.Fatalf("replayed %d records (torn=%v), want %d", len(got), res.TornTail, len(want))
	}
	for i := range want {
		if !sameRecord(got[i], want[i]) {
			t.Fatalf("record %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestTornTailIsIgnored(t *testing.T) {
	path, want := writeLog(t, 10)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the last record in half, as a crash mid-write would.
	if err := os.Truncate(path, info.Size()-5); err != nil {
		t.Fatal(err)
	}
	got, res := collect(t, path)
	if !res.TornTail || len(got) != len(want)-1 {
		t.Fatalf("got %d records torn=%v, want %d torn=true", len(got), res.TornTail, len(want)-1)
	}
}

func TestChecksumMismatchStopsReplay(t *testing.T) {
	path, _ := writeLog(t, 10)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, res := collect(t, path)
	if !res.TornTail || len(got) != 9 {
		t.Fatalf("got %d records torn=%v, want 9 torn=true", len(got), res.TornTail)
	}
}

func FuzzReplay(f *testing.F) {
	f.Add([]byte{})
	f.Add(encodePayload(make([]byte, headerLen), Record{Kind: ikey.KindSet, Seq: 1, Key: []byte("k"), Value: []byte("v")}))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Arbitrary bytes must never panic or error from an in-memory reader.
		if _, err := replayFrom(bytes.NewReader(data), func(Record) error { return nil }); err != nil {
			t.Fatal(err)
		}
	})
}

// frameRaw wraps payload in a header with a valid checksum.
func frameRaw(payload []byte) []byte {
	hdr := make([]byte, headerLen)
	binary.LittleEndian.PutUint32(hdr[0:4], crc32.Checksum(payload, castagnoli))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(hdr, payload...)
}

func TestPayloadWithTrailingBytesIsCorrupt(t *testing.T) {
	good := encodePayload(nil, Record{Kind: ikey.KindSet, Seq: 1, Key: []byte("k"), Value: []byte("v")})
	data := append(frameRaw(good), frameRaw(append(bytes.Clone(good), 0x00))...)
	var n int
	res, err := replayFrom(bytes.NewReader(data), func(Record) error { n++; return nil })
	if err != nil || n != 1 || !res.TornTail {
		t.Fatalf("replayed %d records torn=%v err=%v; want 1 record then a torn tail", n, res.TornTail, err)
	}
}

func TestOversizedLengthIsNotAllocated(t *testing.T) {
	hdr := make([]byte, headerLen)
	binary.LittleEndian.PutUint32(hdr[4:8], maxPayload+1)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	res, err := replayFrom(bytes.NewReader(hdr), func(Record) error { return nil })
	runtime.ReadMemStats(&after)
	if err != nil || !res.TornTail {
		t.Fatalf("torn=%v err=%v; want a torn tail", res.TornTail, err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("a corrupt length allocated %d bytes; want it refused before allocating", grew)
	}
}
