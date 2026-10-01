// Command lsm-kv-store is a small CLI over the store: put, get and delete.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	lsmkv "github.com/EquinoxWN/lsm-kv-store"
)

// usage prints the command synopsis.
func usage() {
	fmt.Fprintln(os.Stderr, "usage: lsm-kv-store [-dir DIR] put KEY VALUE | get KEY | delete KEY | stats")
	flag.PrintDefaults()
}

func main() {
	dir := flag.String("dir", "data", "database directory")
	flag.Usage = usage
	flag.Parse()
	if err := run(*dir, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run executes one subcommand against the store in dir.
func run(dir string, args []string) (err error) {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}
	db, err := lsmkv.Open(dir, lsmkv.Options{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()

	switch {
	case args[0] == "put" && len(args) == 3:
		return db.Put([]byte(args[1]), []byte(args[2]))
	case args[0] == "get" && len(args) == 2:
		v, err := db.Get([]byte(args[1]))
		if err != nil {
			return err
		}
		fmt.Println(string(v))
		return nil
	case args[0] == "delete" && len(args) == 2:
		return db.Delete([]byte(args[1]))
	case args[0] == "stats" && len(args) == 1:
		s := db.Stats()
		fmt.Printf("tables=%d memtable_bytes=%d last_seq=%d\n", s.Tables, s.MemtableBytes, s.LastSeq)
		return nil
	}
	usage()
	return fmt.Errorf("bad command %q", args)
}
