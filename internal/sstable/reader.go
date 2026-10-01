package sstable

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sort"

	"github.com/EquinoxWN/lsm-kv-store/internal/bloom"
	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

type indexEntry struct {
	last   ikey.Key
	off, n uint64
}

// Reader serves point lookups from one table; safe for concurrent use.
type Reader struct {
	f      *os.File
	index  []indexEntry
	filter bloom.Filter
	maxSeq uint64
}

// Open loads the footer, index and bloom filter of the table at path.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := load(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("sstable %s: %w", path, err)
	}
	return r, nil
}

// load reads the table metadata from an open file.
func load(f *os.File) (*Reader, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < footerLen {
		return nil, ErrCorrupt
	}
	buf := make([]byte, footerLen)
	if _, err := f.ReadAt(buf, info.Size()-footerLen); err != nil {
		return nil, err
	}
	ft, err := decodeFooter(buf)
	if err != nil {
		return nil, err
	}
	end := uint64(info.Size() - footerLen)
	if ft.indexOff+ft.indexLen+crcLen > end || ft.bloomOff+ft.bloomLen+crcLen > end {
		return nil, ErrCorrupt
	}
	r := &Reader{f: f, maxSeq: ft.maxSeq}
	idx, err := r.readBlock(ft.indexOff, ft.indexLen)
	if err != nil {
		return nil, err
	}
	if r.index, err = parseIndex(idx); err != nil {
		return nil, err
	}
	filter, err := r.readBlock(ft.bloomOff, ft.bloomLen)
	if err != nil {
		return nil, err
	}
	r.filter = bloom.Filter(filter)
	return r, nil
}

// parseIndex decodes the index block into memory.
func parseIndex(b []byte) ([]indexEntry, error) {
	var out []indexEntry
	for len(b) > 0 {
		enc, rest, ok := readField(b)
		if !ok {
			return nil, ErrCorrupt
		}
		k, err := ikey.Decode(enc)
		if err != nil {
			return nil, ErrCorrupt
		}
		off, w1 := binary.Uvarint(rest)
		if w1 <= 0 {
			return nil, ErrCorrupt
		}
		n, w2 := binary.Uvarint(rest[w1:])
		if w2 <= 0 {
			return nil, ErrCorrupt
		}
		out = append(out, indexEntry{last: k, off: off, n: n})
		b = rest[w1+w2:]
	}
	return out, nil
}

// readBlock reads a block and verifies its checksum.
func (r *Reader) readBlock(off, n uint64) ([]byte, error) {
	buf := make([]byte, n+crcLen)
	if _, err := r.f.ReadAt(buf, int64(off)); err != nil {
		return nil, err
	}
	data, sum := buf[:n], binary.LittleEndian.Uint32(buf[n:])
	if crc32.Checksum(data, castagnoli) != sum {
		return nil, ErrCorrupt
	}
	return data, nil
}

// MayContain consults the bloom filter for user.
func (r *Reader) MayContain(user []byte) bool { return r.filter.MayContain(user) }

// MaxSeq returns the highest sequence number stored in the table.
func (r *Reader) MaxSeq() uint64 { return r.maxSeq }

// Get returns the newest version of user visible at seq.
func (r *Reader) Get(user []byte, seq uint64) (value []byte, kind ikey.Kind, ok bool, err error) {
	if !r.filter.MayContain(user) {
		return nil, 0, false, nil
	}
	target := ikey.Seek(user, seq)
	i := sort.Search(len(r.index), func(i int) bool { return ikey.Compare(r.index[i].last, target) >= 0 })
	if i == len(r.index) {
		return nil, 0, false, nil
	}
	block, err := r.readBlock(r.index[i].off, r.index[i].n)
	if err != nil {
		return nil, 0, false, err
	}
	for len(block) > 0 {
		enc, rest, ok1 := readField(block)
		if !ok1 {
			return nil, 0, false, ErrCorrupt
		}
		val, rest2, ok2 := readField(rest)
		if !ok2 {
			return nil, 0, false, ErrCorrupt
		}
		k, derr := ikey.Decode(enc)
		if derr != nil {
			return nil, 0, false, ErrCorrupt
		}
		if ikey.Compare(k, target) >= 0 {
			if !bytes.Equal(k.User, user) {
				return nil, 0, false, nil
			}
			return bytes.Clone(val), k.Kind, true, nil
		}
		block = rest2
	}
	return nil, 0, false, nil
}

// Close releases the file handle.
func (r *Reader) Close() error { return r.f.Close() }
