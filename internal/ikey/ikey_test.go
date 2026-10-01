package ikey

import (
	"bytes"
	"testing"
)

func TestCompareOrdersNewestFirst(t *testing.T) {
	a := Key{User: []byte("k"), Seq: 5}
	b := Key{User: []byte("k"), Seq: 3}
	c := Key{User: []byte("l"), Seq: 9}
	if Compare(a, b) >= 0 {
		t.Fatal("newer version must sort first")
	}
	if Compare(b, c) >= 0 {
		t.Fatal("user key order must dominate")
	}
	if Compare(a, a) != 0 {
		t.Fatal("key must equal itself")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	in := Key{User: []byte("hello"), Seq: MaxSeq, Kind: KindDelete}
	out, err := Decode(Append(nil, in))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.User, in.User) || out.Seq != in.Seq || out.Kind != in.Kind {
		t.Fatalf("got %+v want %+v", out, in)
	}
}

func TestDecodeShort(t *testing.T) {
	if _, err := Decode([]byte{1, 2}); err != ErrShort {
		t.Fatalf("want ErrShort, got %v", err)
	}
}
