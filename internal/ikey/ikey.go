// Package ikey defines internal keys: a user key plus a sequence number and kind.
package ikey

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Kind says whether an entry stores a value or deletes the key.
type Kind uint8

const (
	KindDelete Kind = 0
	KindSet    Kind = 1
)

// MaxSeq is the largest sequence number that fits in a trailer.
const MaxSeq = uint64(1)<<56 - 1

// TrailerLen is the size of the encoded (seq, kind) suffix.
const TrailerLen = 8

// ErrShort reports an encoded key without a full trailer.
var ErrShort = errors.New("ikey: encoded key too short")

// Key is a user key tagged with the sequence number that wrote it.
type Key struct {
	User []byte
	Seq  uint64
	Kind Kind
}

// Compare orders by user key ascending, then newest sequence first.
func Compare(a, b Key) int {
	if c := bytes.Compare(a.User, b.User); c != 0 {
		return c
	}
	switch {
	case a.Seq > b.Seq:
		return -1
	case a.Seq < b.Seq:
		return 1
	}
	return 0
}

// Seek returns the first possible key for user that is visible at seq.
func Seek(user []byte, seq uint64) Key {
	return Key{User: user, Seq: seq, Kind: KindSet}
}

// Append encodes k onto dst as user key followed by an 8-byte trailer.
func Append(dst []byte, k Key) []byte {
	dst = append(dst, k.User...)
	return binary.LittleEndian.AppendUint64(dst, k.Seq<<8|uint64(k.Kind))
}

// Decode parses an encoded key; the result aliases b.
func Decode(b []byte) (Key, error) {
	if len(b) < TrailerLen {
		return Key{}, ErrShort
	}
	n := len(b) - TrailerLen
	t := binary.LittleEndian.Uint64(b[n:])
	return Key{User: b[:n:n], Seq: t >> 8, Kind: Kind(t & 0xff)}, nil
}
