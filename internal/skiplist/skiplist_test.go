package skiplist

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

func TestAllIsSorted(t *testing.T) {
	l := New()
	r := rand.New(rand.NewPCG(7, 7))
	for seq := uint64(1); seq <= 2000; seq++ {
		k := fmt.Sprintf("key-%04d", r.IntN(500))
		l.Put(ikey.Key{User: []byte(k), Seq: seq, Kind: ikey.KindSet}, []byte(k))
	}
	var keys []ikey.Key
	for k := range l.All() {
		keys = append(keys, k)
	}
	if len(keys) != 2000 || l.Len() != 2000 {
		t.Fatalf("got %d entries, want 2000", len(keys))
	}
	if !slices.IsSortedFunc(keys, ikey.Compare) {
		t.Fatal("entries are not in internal-key order")
	}
}

func TestGetRespectsSequence(t *testing.T) {
	l := New()
	l.Put(ikey.Key{User: []byte("a"), Seq: 1, Kind: ikey.KindSet}, []byte("v1"))
	l.Put(ikey.Key{User: []byte("a"), Seq: 3, Kind: ikey.KindSet}, []byte("v3"))
	l.Put(ikey.Key{User: []byte("a"), Seq: 5, Kind: ikey.KindDelete}, nil)

	cases := []struct {
		seq  uint64
		want string
		kind ikey.Kind
	}{
		{1, "v1", ikey.KindSet},
		{2, "v1", ikey.KindSet},
		{4, "v3", ikey.KindSet},
		{9, "", ikey.KindDelete},
	}
	for _, c := range cases {
		v, kind, ok := l.Get([]byte("a"), c.seq)
		if !ok || string(v) != c.want || kind != c.kind {
			t.Errorf("seq %d: got (%q, %d, %v)", c.seq, v, kind, ok)
		}
	}
	if _, _, ok := l.Get([]byte("a"), 0); ok {
		t.Error("nothing is visible before the first write")
	}
	if _, _, ok := l.Get([]byte("b"), 9); ok {
		t.Error("missing key must not be found")
	}
}

func TestPutCopiesInput(t *testing.T) {
	l := New()
	k, v := []byte("key"), []byte("val")
	l.Put(ikey.Key{User: k, Seq: 1, Kind: ikey.KindSet}, v)
	k[0], v[0] = 'X', 'X'
	got, _, ok := l.Get([]byte("key"), 1)
	if !ok || string(got) != "val" {
		t.Fatalf("list must own its copies, got %q", got)
	}
}
