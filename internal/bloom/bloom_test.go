package bloom

import (
	"fmt"
	"testing"
)

func keys(prefix string, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf("%s-%d", prefix, i))
	}
	return out
}

func TestNoFalseNegatives(t *testing.T) {
	in := keys("present", 10000)
	f := Build(in, 10)
	for _, k := range in {
		if !f.MayContain(k) {
			t.Fatalf("false negative for %s", k)
		}
	}
}

func TestFalsePositiveRate(t *testing.T) {
	f := Build(keys("present", 10000), 10)
	fp := 0
	probe := keys("absent", 10000)
	for _, k := range probe {
		if f.MayContain(k) {
			fp++
		}
	}
	// Theory gives about 0.8% at 10 bits per key; allow slack for hash quality.
	if rate := float64(fp) / float64(len(probe)); rate > 0.02 {
		t.Fatalf("false-positive rate %.4f exceeds 2%%", rate)
	}
}

func TestEmptyFilterIsPermissive(t *testing.T) {
	if !Filter(nil).MayContain([]byte("x")) {
		t.Fatal("a missing filter must not hide keys")
	}
}
