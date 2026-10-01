// Package skiplist is the sorted in-memory table that absorbs writes before a flush.
package skiplist

import (
	"bytes"
	"iter"
	"math/rand/v2"
	"sync"

	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
)

const (
	maxHeight = 12
	branching = 4
	// nodeOverhead approximates per-entry bookkeeping for size accounting.
	nodeOverhead = 48
)

type node struct {
	key   ikey.Key
	value []byte
	next  [maxHeight]*node
}

// List is a concurrency-safe skip list ordered by ikey.Compare.
type List struct {
	mu     sync.RWMutex
	head   node
	height int
	rnd    *rand.Rand
	size   int
	count  int
}

// New returns an empty list.
func New() *List {
	return &List{height: 1, rnd: rand.New(rand.NewPCG(1, 2))}
}

// randomHeight picks a tower height with P(h+1) = 1/branching.
func (l *List) randomHeight() int {
	h := 1
	for h < maxHeight && l.rnd.IntN(branching) == 0 {
		h++
	}
	return h
}

// findGE returns the first node >= k and fills prev with its predecessors.
func (l *List) findGE(k ikey.Key, prev *[maxHeight]*node) *node {
	x := &l.head
	for level := l.height - 1; level >= 0; level-- {
		for n := x.next[level]; n != nil && ikey.Compare(n.key, k) < 0; n = x.next[level] {
			x = n
		}
		if prev != nil {
			prev[level] = x
		}
	}
	return x.next[0]
}

// Put inserts a new version; key and value are copied.
func (l *List) Put(k ikey.Key, value []byte) {
	k.User = bytes.Clone(k.User)
	value = bytes.Clone(value)

	l.mu.Lock()
	defer l.mu.Unlock()

	var prev [maxHeight]*node
	l.findGE(k, &prev)
	h := l.randomHeight()
	if h > l.height {
		for i := l.height; i < h; i++ {
			prev[i] = &l.head
		}
		l.height = h
	}
	n := &node{key: k, value: value}
	for i := 0; i < h; i++ {
		n.next[i] = prev[i].next[i]
		prev[i].next[i] = n
	}
	l.size += len(k.User) + len(value) + nodeOverhead
	l.count++
}

// Get returns the newest version of user visible at seq.
func (l *List) Get(user []byte, seq uint64) (value []byte, kind ikey.Kind, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	n := l.findGE(ikey.Seek(user, seq), nil)
	if n == nil || !bytes.Equal(n.key.User, user) {
		return nil, 0, false
	}
	return n.value, n.key.Kind, true
}

// All yields every entry in internal-key order.
func (l *List) All() iter.Seq2[ikey.Key, []byte] {
	return func(yield func(ikey.Key, []byte) bool) {
		l.mu.RLock()
		defer l.mu.RUnlock()
		for n := l.head.next[0]; n != nil; n = n.next[0] {
			if !yield(n.key, n.value) {
				return
			}
		}
	}
}

// Size approximates the memory held by entries, in bytes.
func (l *List) Size() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.size
}

// Len returns the number of entries.
func (l *List) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.count
}
