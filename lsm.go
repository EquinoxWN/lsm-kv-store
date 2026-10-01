// Package lsmkv is a log-structured merge-tree key-value store that never loses an acknowledged write.
package lsmkv

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/EquinoxWN/lsm-kv-store/internal/ikey"
	"github.com/EquinoxWN/lsm-kv-store/internal/skiplist"
	"github.com/EquinoxWN/lsm-kv-store/internal/sstable"
	"github.com/EquinoxWN/lsm-kv-store/internal/wal"
)

var (
	ErrNotFound = errors.New("lsmkv: key not found")
	ErrClosed   = errors.New("lsmkv: database closed")
	ErrEmptyKey = errors.New("lsmkv: empty key")
	ErrTooLarge = errors.New("lsmkv: entry too large")
)

const maxEntry = 32 << 20

// Options configures a DB; zero values pick the defaults.
type Options struct {
	// MemtableSize is the approximate byte size that triggers a flush (default 4 MiB).
	MemtableSize int
	// BlockSize is the target SSTable data block size (default 4 KiB).
	BlockSize int
	// BloomBitsPerKey sizes each table's bloom filter (default 10, about 1% false positives).
	BloomBitsPerKey int
	// NoSync skips the per-write fsync; only for benchmarks, it gives up durability.
	NoSync bool
}

// withDefaults fills unset options.
func (o Options) withDefaults() Options {
	if o.MemtableSize <= 0 {
		o.MemtableSize = 4 << 20
	}
	if o.BlockSize <= 0 {
		o.BlockSize = 4 << 10
	}
	if o.BloomBitsPerKey <= 0 {
		o.BloomBitsPerKey = 10
	}
	return o
}

type memtable struct {
	list   *skiplist.List
	log    *wal.Writer
	logNum uint64
}

type table struct {
	num uint64
	r   *sstable.Reader
}

// Stats is a point-in-time view of the store's shape.
type Stats struct {
	Tables        int
	MemtableBytes int
	LastSeq       uint64
}

// DB is a single-directory LSM store; all methods are safe for concurrent use.
type DB struct {
	dir  string
	opts Options

	mu      sync.Mutex
	flushed *sync.Cond
	mem     *memtable
	imm     *memtable
	tables  []table // L0, newest first; replaced, never mutated in place
	seq     uint64
	nextNum uint64
	bgErr   error
	closed  bool

	// readMu lets Close wait for in-flight reads before closing files.
	readMu   sync.RWMutex
	flushReq chan struct{}
	done     chan struct{}
}

// Open opens or creates a store in dir and recovers any unflushed writes.
func Open(dir string, opts Options) (*DB, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db := &DB{dir: dir, opts: opts, flushReq: make(chan struct{}, 1), done: make(chan struct{})}
	db.flushed = sync.NewCond(&db.mu)
	if err := db.recover(); err != nil {
		db.closeTables()
		return nil, err
	}
	go db.flushLoop()
	return db, nil
}

// recover loads tables, replays logs, flushes them, and starts a fresh log.
func (db *DB) recover() error {
	entries, err := os.ReadDir(db.dir)
	if err != nil {
		return err
	}
	var logs, tables []uint64
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			// A half-written table from an interrupted flush; its log is still present.
			if err := os.Remove(filepath.Join(db.dir, e.Name())); err != nil {
				return err
			}
			continue
		}
		num, kind, ok := parseFileName(e.Name())
		if !ok {
			continue
		}
		db.nextNum = max(db.nextNum, num+1)
		if kind == kindLog {
			logs = append(logs, num)
		} else {
			tables = append(tables, num)
		}
	}

	slices.Sort(tables)
	var tableSeq uint64
	for _, num := range slices.Backward(tables) {
		r, err := sstable.Open(fileName(db.dir, num, kindTable))
		if err != nil {
			return err
		}
		db.tables = append(db.tables, table{num: num, r: r})
		tableSeq = max(tableSeq, r.MaxSeq())
	}
	db.seq = tableSeq

	slices.Sort(logs)
	recovered := skiplist.New()
	for _, num := range logs {
		_, err := wal.Replay(fileName(db.dir, num, kindLog), func(r wal.Record) error {
			// Records at or below tableSeq were flushed before a crash hid the log removal.
			if r.Seq > tableSeq {
				recovered.Put(ikey.Key{User: r.Key, Seq: r.Seq, Kind: r.Kind}, r.Value)
				db.seq = max(db.seq, r.Seq)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if recovered.Len() > 0 {
		num := db.nextNum
		db.nextNum++
		r, err := db.writeTable(recovered, num)
		if err != nil {
			return err
		}
		db.tables = append([]table{{num: num, r: r}}, db.tables...)
	}
	for _, num := range logs {
		if err := os.Remove(fileName(db.dir, num, kindLog)); err != nil {
			return err
		}
	}
	if err := syncDir(db.dir); err != nil {
		return err
	}
	return db.newMemtable()
}

// newMemtable installs an empty memtable backed by a new log.
func (db *DB) newMemtable() error {
	num := db.nextNum
	db.nextNum++
	log, err := wal.Create(fileName(db.dir, num, kindLog), !db.opts.NoSync)
	if err != nil {
		return err
	}
	db.mem = &memtable{list: skiplist.New(), log: log, logNum: num}
	return syncDir(db.dir)
}

// writeTable flushes list to a durable, fully written SSTable and opens it.
func (db *DB) writeTable(list *skiplist.List, num uint64) (*sstable.Reader, error) {
	final := fileName(db.dir, num, kindTable)
	tmp := final + ".tmp"
	w, err := sstable.Create(tmp, sstable.WriterOptions{BlockSize: db.opts.BlockSize, BloomBitsPerKey: db.opts.BloomBitsPerKey})
	if err != nil {
		return nil, err
	}
	for k, v := range list.All() {
		if err := w.Add(k, v); err != nil {
			w.Abort()
			os.Remove(tmp)
			return nil, err
		}
	}
	if err := w.Finish(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	if err := syncDir(db.dir); err != nil {
		return nil, err
	}
	return sstable.Open(final)
}

// flushLoop turns each immutable memtable into an SSTable in the background.
func (db *DB) flushLoop() {
	defer close(db.done)
	for range db.flushReq {
		db.mu.Lock()
		imm := db.imm
		num := db.nextNum
		db.nextNum++
		db.mu.Unlock()
		if imm == nil {
			continue
		}

		r, err := db.writeTable(imm.list, num)

		db.mu.Lock()
		if err != nil {
			db.bgErr = fmt.Errorf("lsmkv: flush: %w", err)
		} else {
			db.tables = append([]table{{num: num, r: r}}, db.tables...)
			db.imm = nil
			// A leftover log is harmless: recovery skips records already in tables.
			_ = os.Remove(fileName(db.dir, imm.logNum, kindLog))
		}
		db.flushed.Broadcast()
		db.mu.Unlock()
	}
}

// makeRoom rotates a full memtable, waiting if the previous flush is still running.
func (db *DB) makeRoom() error {
	for {
		switch {
		case db.closed:
			return ErrClosed
		case db.bgErr != nil:
			return db.bgErr
		case db.mem.list.Size() < db.opts.MemtableSize:
			return nil
		case db.imm != nil:
			db.flushed.Wait()
			continue
		}
		old := db.mem
		if err := db.newMemtable(); err != nil {
			return err
		}
		if err := old.log.Close(); err != nil {
			db.bgErr = fmt.Errorf("lsmkv: close log: %w", err)
			return db.bgErr
		}
		old.log = nil
		db.imm = old
		select {
		case db.flushReq <- struct{}{}:
		default:
		}
		return nil
	}
}

// write logs and applies one mutation.
func (db *DB) write(kind ikey.Kind, key, value []byte) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if len(key)+len(value) > maxEntry {
		return ErrTooLarge
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.makeRoom(); err != nil {
		return err
	}
	seq := db.seq + 1
	if err := db.mem.log.Append(wal.Record{Kind: kind, Seq: seq, Key: key, Value: value}); err != nil {
		// The log may now hold a partial record, so stop accepting writes.
		db.bgErr = fmt.Errorf("lsmkv: wal append: %w", err)
		return db.bgErr
	}
	db.seq = seq
	db.mem.list.Put(ikey.Key{User: key, Seq: seq, Kind: kind}, value)
	return nil
}

// Put stores value under key; it returns once the write is durable.
func (db *DB) Put(key, value []byte) error { return db.write(ikey.KindSet, key, value) }

// Delete removes key by writing a tombstone.
func (db *DB) Delete(key []byte) error { return db.write(ikey.KindDelete, key, nil) }

// Get returns the latest value for key or ErrNotFound.
func (db *DB) Get(key []byte) ([]byte, error) {
	db.readMu.RLock()
	defer db.readMu.RUnlock()

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil, ErrClosed
	}
	mem, imm, tables, seq := db.mem, db.imm, db.tables, db.seq
	db.mu.Unlock()

	for _, m := range []*memtable{mem, imm} {
		if m == nil {
			continue
		}
		if v, kind, ok := m.list.Get(key, seq); ok {
			return found(bytes.Clone(v), kind)
		}
	}
	for _, t := range tables {
		v, kind, ok, err := t.r.Get(key, seq)
		if err != nil {
			return nil, err
		}
		if ok {
			return found(v, kind)
		}
	}
	return nil, ErrNotFound
}

// found converts a located entry into Get's result.
func found(v []byte, kind ikey.Kind) ([]byte, error) {
	if kind == ikey.KindDelete {
		return nil, ErrNotFound
	}
	return v, nil
}

// Stats reports table count, memtable size and the last sequence number.
func (db *DB) Stats() Stats {
	db.mu.Lock()
	defer db.mu.Unlock()
	return Stats{Tables: len(db.tables), MemtableBytes: db.mem.list.Size(), LastSeq: db.seq}
}

// Close waits for a pending flush, then releases files; the active log is replayed on next Open.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	for db.imm != nil && db.bgErr == nil {
		db.flushed.Wait()
	}
	close(db.flushReq)
	db.flushed.Broadcast()
	db.mu.Unlock()
	<-db.done

	db.readMu.Lock()
	defer db.readMu.Unlock()
	var errs []error
	if db.mem != nil && db.mem.log != nil {
		errs = append(errs, db.mem.log.Close())
	}
	errs = append(errs, db.closeTables())
	return errors.Join(errs...)
}

// closeTables closes every open table reader.
func (db *DB) closeTables() error {
	var errs []error
	for _, t := range db.tables {
		errs = append(errs, t.r.Close())
	}
	db.tables = nil
	return errors.Join(errs...)
}
