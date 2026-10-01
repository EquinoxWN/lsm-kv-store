// Package sstable writes and reads immutable sorted tables.
//
// File layout:
//
//	data block 0 | crc | ... | data block n | crc | index block | crc | bloom block | crc | footer
//
// A data block holds entries "uvarint klen | internal key | uvarint vlen | value".
// The index holds one entry per data block: "uvarint klen | last key | uvarint off | uvarint len".
// The footer is six uint64 LE: indexOff, indexLen, bloomOff, bloomLen, maxSeq, magic.
package sstable

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	footerLen = 48
	crcLen    = 4
	magic     = uint64(0x4c534d5353543031) // "LSMSST01"
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// ErrCorrupt reports a table that fails a checksum or structural check.
var ErrCorrupt = errors.New("sstable: corrupt table")

type footer struct {
	indexOff, indexLen uint64
	bloomOff, bloomLen uint64
	maxSeq             uint64
}

// encode serialises the footer with the magic number.
func (f footer) encode() []byte {
	b := make([]byte, 0, footerLen)
	for _, v := range []uint64{f.indexOff, f.indexLen, f.bloomOff, f.bloomLen, f.maxSeq, magic} {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	return b
}

// decodeFooter parses and validates a footer.
func decodeFooter(b []byte) (footer, error) {
	if len(b) != footerLen || binary.LittleEndian.Uint64(b[40:]) != magic {
		return footer{}, ErrCorrupt
	}
	u := func(i int) uint64 { return binary.LittleEndian.Uint64(b[i*8:]) }
	return footer{indexOff: u(0), indexLen: u(1), bloomOff: u(2), bloomLen: u(3), maxSeq: u(4)}, nil
}

// readField reads a uvarint-prefixed byte string.
func readField(b []byte) (field, rest []byte, ok bool) {
	n, w := binary.Uvarint(b)
	if w <= 0 || n > uint64(len(b)-w) {
		return nil, nil, false
	}
	end := w + int(n)
	return b[w:end:end], b[end:], true
}
