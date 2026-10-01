package lsmkv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type fileKind int

const (
	kindLog fileKind = iota
	kindTable
)

// fileName builds the path of a numbered log or table.
func fileName(dir string, num uint64, k fileKind) string {
	ext := ".log"
	if k == kindTable {
		ext = ".sst"
	}
	return filepath.Join(dir, fmt.Sprintf("%06d%s", num, ext))
}

// parseFileName recognises "NNNNNN.log" and "NNNNNN.sst".
func parseFileName(name string) (num uint64, k fileKind, ok bool) {
	base, ext, found := strings.Cut(name, ".")
	if !found {
		return 0, 0, false
	}
	switch ext {
	case "log":
		k = kindLog
	case "sst":
		k = kindTable
	default:
		return 0, 0, false
	}
	n, err := strconv.ParseUint(base, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return n, k, true
}

// syncDir makes renames and deletes in dir durable; Windows has no directory fsync.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
