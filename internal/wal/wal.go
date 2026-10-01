// Package wal is the write-ahead log that makes every acknowledged write durable.
//
// Record layout: crc32c(payload) uint32 LE | len(payload) uint32 LE | payload.
// Payload layout: kind byte | seq uint64 LE | uvarint key len | key | uvarint value len | value.
package wal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

const headerLen = 8

// maxPayload bounds a record so a corrupt length cannot trigger a huge allocation.
const maxPayload = 64 << 20

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// errCorrupt marks a record that fails validation.
var errCorrupt = errors.New("wal: corrupt record")

// Record is one logged mutation.
type Record struct {
	Kind  ikey.Kind
	Seq   uint64
	Key   []byte
	Value []byte
}

// Writer appends records to a log file.
type Writer struct {
	f    *os.File
	buf  *bufio.Writer
	sync bool
	scr  []byte
}

// Create opens a new, empty log at path.
func Create(path string, syncEachWrite bool) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, buf: bufio.NewWriterSize(f, 64<<10), sync: syncEachWrite}, nil
}

// encodePayload serialises r onto dst.
func encodePayload(dst []byte, r Record) []byte {
	dst = append(dst, byte(r.Kind))
	dst = binary.LittleEndian.AppendUint64(dst, r.Seq)
	dst = binary.AppendUvarint(dst, uint64(len(r.Key)))
	dst = append(dst, r.Key...)
	dst = binary.AppendUvarint(dst, uint64(len(r.Value)))
	return append(dst, r.Value...)
}

// Append writes r and, when sync is on, fsyncs before returning.
func (w *Writer) Append(r Record) error {
	w.scr = append(w.scr[:0], make([]byte, headerLen)...)
	w.scr = encodePayload(w.scr, r)
	payload := w.scr[headerLen:]
	binary.LittleEndian.PutUint32(w.scr[0:4], crc32.Checksum(payload, castagnoli))
	binary.LittleEndian.PutUint32(w.scr[4:8], uint32(len(payload)))
	if _, err := w.buf.Write(w.scr); err != nil {
		return err
	}
	if err := w.buf.Flush(); err != nil {
		return err
	}
	if w.sync {
		return w.f.Sync()
	}
	return nil
}

// Close flushes, syncs and closes the file.
func (w *Writer) Close() error {
	err := w.buf.Flush()
	if serr := w.f.Sync(); err == nil {
		err = serr
	}
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// decodePayload parses one payload; slices alias p.
func decodePayload(p []byte) (Record, error) {
	if len(p) < 9 {
		return Record{}, errCorrupt
	}
	r := Record{Kind: ikey.Kind(p[0]), Seq: binary.LittleEndian.Uint64(p[1:9])}
	if r.Kind != ikey.KindSet && r.Kind != ikey.KindDelete {
		return Record{}, errCorrupt
	}
	rest := p[9:]
	var ok bool
	if r.Key, rest, ok = readBytes(rest); !ok {
		return Record{}, errCorrupt
	}
	if r.Value, rest, ok = readBytes(rest); !ok || len(rest) != 0 {
		return Record{}, errCorrupt
	}
	return r, nil
}

// readBytes reads a uvarint-prefixed byte string.
func readBytes(b []byte) (field, rest []byte, ok bool) {
	n, w := binary.Uvarint(b)
	if w <= 0 || n > uint64(len(b)-w) {
		return nil, nil, false
	}
	end := w + int(n)
	return b[w:end:end], b[end:], true
}

// ReplayResult summarises a replay.
type ReplayResult struct {
	Records int
	// TornTail is true when the log ended in a partial or corrupt record.
	TornTail bool
}

// Replay calls fn for each valid record and stops quietly at a torn tail.
func Replay(path string, fn func(Record) error) (ReplayResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReplayResult{}, err
	}
	defer f.Close()
	return replayFrom(bufio.NewReader(f), fn)
}

// replayFrom reads records until EOF or the first invalid one.
func replayFrom(r io.Reader, fn func(Record) error) (ReplayResult, error) {
	var res ReplayResult
	var hdr [headerLen]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return res, nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				res.TornTail = true
				return res, nil
			}
			return res, err
		}
		sum := binary.LittleEndian.Uint32(hdr[0:4])
		n := binary.LittleEndian.Uint32(hdr[4:8])
		if n > maxPayload {
			res.TornTail = true
			return res, nil
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				res.TornTail = true
				return res, nil
			}
			return res, err
		}
		if crc32.Checksum(payload, castagnoli) != sum {
			res.TornTail = true
			return res, nil
		}
		rec, err := decodePayload(payload)
		if err != nil {
			res.TornTail = true
			return res, nil
		}
		if err := fn(rec); err != nil {
			return res, fmt.Errorf("wal: apply record %d: %w", res.Records, err)
		}
		res.Records++
	}
}
