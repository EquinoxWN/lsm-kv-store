package sstable

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"github.com/EquinoxWN/lsm-kv-store/internal/bloom"
	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

// WriterOptions tunes table layout.
type WriterOptions struct {
	BlockSize       int
	BloomBitsPerKey int
}

// Writer builds a table from entries added in internal-key order.
type Writer struct {
	f       *os.File
	w       *bufio.Writer
	opts    WriterOptions
	off     uint64
	block   []byte
	index   []byte
	lastKey []byte
	users   [][]byte
	maxSeq  uint64
	entries int
}

// Create starts a new table at path, which must not exist.
func Create(path string, opts WriterOptions) (*Writer, error) {
	if opts.BlockSize <= 0 {
		opts.BlockSize = 4 << 10
	}
	if opts.BloomBitsPerKey <= 0 {
		opts.BloomBitsPerKey = 10
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, w: bufio.NewWriterSize(f, 256<<10), opts: opts}, nil
}

// Add appends one entry; keys must arrive in strictly increasing internal order.
func (w *Writer) Add(k ikey.Key, value []byte) error {
	enc := ikey.Append(nil, k)
	if w.lastKey != nil {
		prev, _ := ikey.Decode(w.lastKey)
		if ikey.Compare(prev, k) >= 0 {
			return fmt.Errorf("sstable: key %q seq %d added out of order", k.User, k.Seq)
		}
	}
	if len(w.users) == 0 || !bytes.Equal(w.users[len(w.users)-1], k.User) {
		w.users = append(w.users, bytes.Clone(k.User))
	}
	w.block = binary.AppendUvarint(w.block, uint64(len(enc)))
	w.block = append(w.block, enc...)
	w.block = binary.AppendUvarint(w.block, uint64(len(value)))
	w.block = append(w.block, value...)
	w.lastKey = enc
	w.maxSeq = max(w.maxSeq, k.Seq)
	w.entries++
	if len(w.block) >= w.opts.BlockSize {
		return w.flushBlock()
	}
	return nil
}

// writeBlock writes b plus its checksum and returns its offset and length.
func (w *Writer) writeBlock(b []byte) (off, n uint64, err error) {
	off = w.off
	if _, err = w.w.Write(b); err != nil {
		return 0, 0, err
	}
	var sum [crcLen]byte
	binary.LittleEndian.PutUint32(sum[:], crc32.Checksum(b, castagnoli))
	if _, err = w.w.Write(sum[:]); err != nil {
		return 0, 0, err
	}
	w.off += uint64(len(b)) + crcLen
	return off, uint64(len(b)), nil
}

// flushBlock writes the pending data block and records it in the index.
func (w *Writer) flushBlock() error {
	if len(w.block) == 0 {
		return nil
	}
	off, n, err := w.writeBlock(w.block)
	if err != nil {
		return err
	}
	w.index = binary.AppendUvarint(w.index, uint64(len(w.lastKey)))
	w.index = append(w.index, w.lastKey...)
	w.index = binary.AppendUvarint(w.index, off)
	w.index = binary.AppendUvarint(w.index, n)
	w.block = w.block[:0]
	return nil
}

// Finish writes index, bloom filter and footer, then fsyncs and closes.
func (w *Writer) Finish() error {
	if w.entries == 0 {
		w.Abort()
		return errors.New("sstable: refusing to write an empty table")
	}
	err := w.flushBlock()
	var ft footer
	if err == nil {
		ft.indexOff, ft.indexLen, err = w.writeBlock(w.index)
	}
	if err == nil {
		ft.bloomOff, ft.bloomLen, err = w.writeBlock(bloom.Build(w.users, w.opts.BloomBitsPerKey))
	}
	if err == nil {
		ft.maxSeq = w.maxSeq
		_, err = w.w.Write(ft.encode())
	}
	if err == nil {
		err = w.w.Flush()
	}
	if err == nil {
		err = w.f.Sync()
	}
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Abort closes the file without finishing it.
func (w *Writer) Abort() { _ = w.f.Close() }
