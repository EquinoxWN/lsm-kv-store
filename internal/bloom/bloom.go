// Package bloom is a Bloom filter that lets reads skip tables that cannot hold a key.
package bloom

import (
	"hash/fnv"
	"math"
)

// Filter is an encoded filter: bit array followed by one byte holding k.
type Filter []byte

// hash64 returns a 64-bit FNV-1a hash of key.
func hash64(key []byte) uint64 {
	h := fnv.New64a()
	h.Write(key)
	return h.Sum64()
}

// Build creates a filter for keys using bitsPerKey bits each.
func Build(keys [][]byte, bitsPerKey int) Filter {
	k := max(1, min(30, int(math.Round(float64(bitsPerKey)*math.Ln2))))
	nbits := max(64, len(keys)*bitsPerKey)
	nbytes := (nbits + 7) / 8
	nbits = nbytes * 8

	f := make(Filter, nbytes+1)
	for _, key := range keys {
		h := hash64(key)
		h1, h2 := uint32(h), uint32(h>>32)|1
		for i := 0; i < k; i++ {
			bit := (h1 + uint32(i)*h2) % uint32(nbits)
			f[bit/8] |= 1 << (bit % 8)
		}
	}
	f[nbytes] = byte(k)
	return f
}

// MayContain reports false only when key is definitely absent.
func (f Filter) MayContain(key []byte) bool {
	if len(f) < 2 {
		return true
	}
	nbits := uint32(len(f)-1) * 8
	k := int(f[len(f)-1])
	h := hash64(key)
	h1, h2 := uint32(h), uint32(h>>32)|1
	for i := 0; i < k; i++ {
		bit := (h1 + uint32(i)*h2) % nbits
		if f[bit/8]&(1<<(bit%8)) == 0 {
			return false
		}
	}
	return true
}
