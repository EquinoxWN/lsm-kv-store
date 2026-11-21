package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

func TestRunLeavesNoStoreForABadCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	if err := run(dir, []string{"frobnicate"}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("bad command created %s (stat err %v)", dir, err)
	}
}
