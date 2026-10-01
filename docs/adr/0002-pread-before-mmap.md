# ADR 0002: Read SSTables with ReadAt before adopting mmap

- **Status:** Accepted

## Context

The tech stack names mmap-backed SSTables. mmap avoids a copy per read, but it needs
platform-specific code (Windows `CreateFileMapping` vs POSIX `mmap`), turns I/O errors into
signals or panics instead of returned errors, and hides page-cache behaviour behind the kernel.
M1 needs correct, checksummed reads on both Windows (development) and Linux (CI).

## Decision

M1 reads blocks with `os.File.ReadAt` (pread), verifies each block's crc32c, and keeps only the
index and bloom filter in memory. mmap is reconsidered in M3, once benchmarks exist to show
whether the copy matters.

## Consequences

- Reads cost one system call and one copy per block. The benchmark baseline makes the later
  mmap comparison honest.
- Corruption and I/O failures surface as ordinary errors (`sstable.ErrCorrupt`).
- A block cache (LRU) becomes the natural next step for hot keys, independent of mmap.
