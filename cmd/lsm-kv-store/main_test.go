package main

import (
	"errors"
	"strings"
	"testing"

	lsmkv "github.com/EquinoxWN/lsm-kv-store"
)

func TestRunStoresReadsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, []string{"put", "k", "v"}); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, []string{"get", "k"}); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, []string{"delete", "k"}); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, []string{"get", "k"}); !errors.Is(err, lsmkv.ErrNotFound) {
		t.Fatalf("get after delete: %v, want ErrNotFound", err)
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{nil, {"put", "k"}, {"get"}, {"frobnicate"}, {"put", "", "v"}} {
		err := run(dir, args)
		if err == nil {
			t.Fatalf("run(%q) succeeded, want an error", args)
		}
		if len(args) == 0 && !strings.Contains(err.Error(), "missing command") {
			t.Fatalf("run(nil) = %v", err)
		}
	}
}
