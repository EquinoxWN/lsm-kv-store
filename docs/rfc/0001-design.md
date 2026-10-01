# RFC 0001: lsm-kv-store design

- **Status:** Accepted (M1 implemented)
- **Author:** AUTHOR_NAME
- **Created:** 2026

## Problem

Write-heavy databases such as Cassandra and RocksDB stay fast because they never update data in
place: they append to a log, buffer in memory and write sorted files in bulk. The hard part is
doing that without losing a write the caller was told had succeeded. This project builds that
engine from scratch and proves the durability claim with crash tests.

## Goals

- **Durability:** once `Put` returns, the write survives a process crash (WAL + fsync).
- **Fast writes:** writes go to an in-memory skip list; disk writes are sequential.
- **Bounded memory:** a full memtable is frozen and flushed to an SSTable in the background.
- **Fast misses:** every SSTable carries a bloom filter, so most absent keys never touch disk.
- **Correct recovery:** on open, logs are replayed and torn tails from a crash are ignored.
- Later milestones: leveled compaction (M2), MVCC snapshots and a crash-injection harness (M3).

## Non-goals

- Networked access, replication or sharding (that is `multi-raft-kv`).
- Range scans and iterators in M1; they arrive with compaction in M2.
- Running as a hosted production service.

## Proposed design

![architecture](../architecture.png)

### Write path (M1)

1. `Put(k, v)` takes the DB mutex, assigns the next sequence number and appends a WAL record
   `crc32c | len | kind, seq, key, value`, then fsyncs.
2. Only after the fsync is the entry inserted into the skip-list memtable, keyed by
   `(user key ascending, seq descending)`. Deletes write a tombstone the same way.
3. When the memtable passes `MemtableSize`, it becomes immutable, a new WAL and memtable are
   installed, and a background goroutine flushes the immutable one. A writer that fills the
   next memtable before the flush ends waits (write stall), which bounds memory.

### SSTable format (M1)

```
data block | crc | ... | index block | crc | bloom block | crc | footer (48 bytes)
```

- Data blocks (~4 KiB) hold sorted `(internal key, value)` entries.
- The sparse index keeps the last key of each block, so a lookup reads exactly one block.
- The bloom filter uses 10 bits per key (~1% false positives) with double hashing.
- The footer stores block offsets, the table's max sequence number and a magic number.
- A table is written to `NNNNNN.sst.tmp`, fsynced, then renamed, so a crash never leaves a
  half-written table that looks complete.

### Read path (M1)

Memtable, then immutable memtable, then L0 tables newest first. The first version found for the
key decides the answer; a tombstone means "not found".

### Recovery (M1)

On open: delete `*.tmp`, open tables, replay each WAL in order stopping at the first torn or
corrupt record, flush the recovered entries to a new table, then delete the logs. Records whose
sequence number is already covered by a table are skipped, so a crash between "table renamed" and
"log deleted" cannot duplicate data.

### Compaction and snapshots (M2, M3)

Leveled compaction merges overlapping tables into the next level, drops shadowed versions and
old tombstones, and records write amplification. Snapshots pin a sequence number (MVCC).

## Alternatives considered

| Option | Why not (yet) |
|---|---|
| B-tree with in-place updates | Random writes and page splits; the point of this project is the write-optimised LSM design. |
| Group commit (one fsync for many writers) | Much higher throughput, but more moving parts. M1 does one fsync per write under the mutex; group commit is an M3 optimisation measured against this baseline. |
| mmap-backed SSTable reads | Planned in the tech stack, but `ReadAt` is portable (Windows and Linux) and simpler to reason about for checksums. See ADR 0002. |
| Size-tiered compaction | Lower write amplification but higher space and read amplification; leveled fits a read-mostly benchmark mix (YCSB B/C). |
| Hash index instead of a skip list | No ordered iteration, which flush and range scans need. |

## Measurement plan

- `make bench`: `BenchmarkPutSync` (durable writes), `BenchmarkPutNoSync`, `BenchmarkGet`.
- M3: YCSB A/B/C throughput and p99 against sled; read, write and space amplification per
  level; a crash-test log showing zero lost acknowledged writes.

## Milestones

- **M1 (done):** WAL + fsync, skip-list memtable, background flush to SSTables with sparse index
  and bloom filter, point reads across memtables and L0, crash recovery.
- **M2:** leveled compaction, per-level read path, write-amplification metrics.
- **M3:** MVCC snapshots, crash-injection harness, published benchmarks.

## Risks and open questions

- One fsync per write caps durable throughput at the disk's fsync rate (about 375 writes/s in a
  first local run on a Windows laptop). Group commit is the planned fix.
- L0 tables overlap, so reads get slower as L0 grows until compaction lands in M2.
- Windows has no directory fsync; on Linux the directory is fsynced after renames and deletes.
