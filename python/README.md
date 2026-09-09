# abstraction-cas

**In development.** Tagged `go/v0.1.0`, but this page predates that tag: no
capability claim, no contract page, no conformance scenario of its own.

A file is replaced whole or not at all, and every change is applied to the value
the previous change left — across processes and across languages sharing one
kernel.

## The problem

Every service that keeps state in a file rewrites it, and two writers on one
file lose each other's updates unless they agree on a lock and a rename. Each
service of ours hand-rolled its own writer. This is the floor under all of them:
one lock, one rename, one implementation per language we ship. Not a
layer — no contract page, no capability claim. A stranger implementing `job`
needs its page, not this.

## Words

| word | meaning |
|---|---|
| **read** | the whole file, or nothing when there is none |
| **write** | replace the file only if it still holds `base`; else `Moved` |
| **change** | read, edit and write under the lock, so an edit always sees the truth and never needs a retry |
| **base** | the value a writer read; `nil` means *only if the file does not exist* |
| **lock** | byte 0 of `<path>.lock`, exclusive, blocking, released by the kernel when the holder dies |

No rule on this page carries a tag. The invariants below each carry a test, and
*What a fourth implementation must do* is cited by
[the job page](https://github.com/openabstractions/abstraction-job).

## Obtain

- **Go.** `go get github.com/openabstractions/abstraction-cas/go`. No tag yet;
  `go get` resolves a pseudo-version of `main`.
- **Python.** Not on any index. `python/abstraction_cas.py` is one module with
  no imports of ours; `python/pyproject.toml` builds a wheel
  (`pip wheel python/`).
- **C++.** One header and one source, C++17, standard library only, and no build
  file: name them. `g++ -std=c++17 -I cpp/include cpp/test/test_cas.cpp
  cpp/src/cas.cpp -o test_cas` compiles and passes this layer's own test; MSVC
  19.51 takes the same two files under `/std:c++17` and `/I`. Measured
  2026-09-09. There
  is no tagged release and no package.

## Example

```go
import cas "github.com/openabstractions/abstraction-cas/go"

err := cas.Change(path, func(cur []byte) ([]byte, error) {
    var f file
    if cur != nil {
        json.Unmarshal(cur, &f)
    }
    f.Apps = append(f.Apps, app)
    return json.Marshal(f)
})
```

`Read` returns the whole file, or `nil` when there is none. `Write(path, base,
data)` replaces the file only if it still holds `base` (`nil`: only if it does
not exist), else `ErrMoved`. `Change` reads, edits and writes under the lock, so
an edit always sees the truth and never needs a retry.

The same three calls in `python/abstraction_cas.py` (`read`, `write`, `change`,
`None` for missing, `Moved`) and `cpp/include/abstraction/cas.h` (`std::nullopt`
for missing, `Moved` thrown, an edit refuses by throwing). All three share one
lock and one rename, so writers in different languages on one file do not lose
each other's updates; `mixed.py` proves it and `scripts/check.sh` runs it.

## Invariants, each with a test in `go/cas_test.go`, `python/test_abstraction_cas.py`, `cpp/test/test_cas.cpp`

- **Whole or nothing.** A reader at any moment, from any process, sees a value
  some writer wrote in full. Never a partial file, never a mixture.
- **No lost update.** Every successful `Change` was applied to the value left
  by the previous successful one, across goroutines and across processes.
- **A stale base is refused** and leaves the file untouched.
- **An ended record stays ended.** An edit that refuses on what it reads is
  refusing on the truth, so nothing can be walked backwards by a writer that
  read earlier.
- **Nothing left behind.** Contention and refusal leave no temporary files.

## How

The data is staged in a temporary file beside the target, fsynced, and renamed
over it, under the lock the contract below names, which the kernel releases
when the holder dies — no lock is ever broken by a timeout. On Windows every
read opens with `FILE_SHARE_DELETE` and the rename is `FileRenameInfoEx` with
POSIX semantics, so a reader of ours never blocks a replace; Go gets both from
`os.Root`, Python and C++ call them. A Samba share answers `FileRenameInfoEx`
with `ERROR_INVALID_PARAMETER`, flag or no flag, so there the fallback runs on
every write — `MoveFileEx` in Python and C++, `FileRenameInformation` in Go —
and a reader does block a replace; see below.

**The lock is one machine's.** A byte-range lock taken over SMB and a `flock`
taken on the server's own volume never meet: writers on the PC and on the NAS
on one file lost or refused 147–149 of 2150 updates in three runs of three,
every PC writer dying within its first few changes
(`feedback/2026-09-06-shareloc.md`, harness `research/locks/twohost.py`). Six
writers in three languages on the PC alone, over the same share, lost nothing
in 1800. Two hosts on one record need a protocol this file does not have.

## What a fourth implementation must do

Two implementations on one file agree on the bytes they write and on the way
they keep each other out. Every single-language test proves the first only: a
writer with another lock passes all of them and loses updates against ours.

- **The lock is byte 0 of `<path>.lock`, exclusive, blocking, no timeout.**
  The lock file sits beside the data file, is opened read-write and created
  `0600` when missing, and is never written, truncated or deleted: a deleted
  lock file is a new inode, and holders of two inodes exclude nobody. Windows
  is `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK)` at offset 0, length 1. POSIX is
  `flock(LOCK_EX)`. Not `fcntl`, `lockf` or an OFD lock: on Linux those are a
  separate lock table, invisible to `flock`. Not `msvcrt.locking`: it retries
  once a second and fails after ten.
- **`Write` and `Change` hold it from before the read to after the rename.
  `Read` never takes it.**
- **Missing and empty are different values**, and a `Change` between them
  writes.
- **The replacement is a fresh temporary file in the same directory, fsynced,
  renamed over `<path>`.** The name is unique to the writer — `CreateTemp`,
  `mkstemp`, `CREATE_NEW` — so two languages staging at once never open each
  other's file. Windows is `FileRenameInfoEx` with `REPLACE_IF_EXISTS |
  POSIX_SEMANTICS`, falling back to `MoveFileEx(REPLACE_EXISTING)` where the
  volume refuses the flags; `ERROR_SHARING_VIOLATION` and `ERROR_ACCESS_DENIED`
  are retried a bounded number of times, then returned with the temporary
  removed. POSIX is `rename(2)`.
- **`Read` opens with every share mode on Windows** and holds the handle for
  the read only.

Prove it by being the seventh process in `mixed.py`: a program that, given
`CAS_PATH`, `CAS_ROLE` and `CAS_N`, applies `CAS_N` increments of the `"n n"`
counter as a `writer` or reads until it sees `CAS_N` as a `reader`, aborting on
a torn read. Add its command to `commands()`; the run counts its updates with
ours. A fourth implementation whose lock ours cannot see loses a third of its
updates on the first run — measured with a Python writer on byte 1 on Windows
and on `lockf` on Linux, in `feedback/2026-09-06-mixedgate.md`. `mixed.py`
takes a directory as its second argument; a UNC path runs the six on a share.

## Today

Go, Python and C++, each the three calls. Consumed by `job`'s file store,
`config`, `rights` and `asks`.

What may break:

- A handle on the data file without delete sharing — an editor, `type`,
  Python's `open()` — makes a Windows replace fail with `ERROR_SHARING_VIOLATION`
  for as long as it is held. The writer retries 1000 times — 160 to 470 ms on
  local NTFS, process start included, the same in all three languages — then
  returns the error with nothing left behind; the lock was held throughout, so
  no other writer's update is lost, and the file still holds the previous
  value. A handle opened with `FILE_SHARE_DELETE` keeps reading the bytes it
  opened while the rename succeeds under it, 2 ms measured.
- Over SMB every open handle on the data file blocks the replace, ours
  included, share-delete or not, from the same client or another process on
  it: the fallback rename fails with `ERROR_ACCESS_DENIED`, 11 to 126 ms a
  try, so the 1000 tries last 25 s in Python and 31 s in C++ under a held
  handle, and Go's `os.Root` rename does not return at all — killed at 90 s,
  its temporary left behind. A reader that polls keeps the tries failing for as
  long as it polls: six writers and three readers of ours on one share stalled
  at 85 and 50 of 600, C++ and Python returned the error, Go never returned. A
  process on the server holding the file open blocks nothing,
  and the server's own rename is never blocked. `Read` over SMB can itself
  fail with `ERROR_ACCESS_DENIED` on open while the server renames underneath
  it, rather than returning either value.
- Every write is an fsync: about 6 ms on this machine. Right for a policy file
  written at human rate; wrong for a lease renewed every millisecond.
- **Python flushes the directory after the rename; Go and C++ do not yet.**
  Without that flush a power cut can lose the rename: the previous value
  survives, the acknowledged one may not. Python opens the directory and
  flushes it — `fsync` on POSIX, `FlushFileBuffers` on a
  `FILE_FLAG_BACKUP_SEMANTICS` handle on Windows — and raises anything it does
  not recognise. Where the volume answers `ERROR_INVALID_FUNCTION`,
  `ERROR_NOT_SUPPORTED` or `ERROR_ACCESS_DENIED` — Samba over SMB is the one we
  have met — the write is acknowledged without that guarantee. The flush costs
  0.1 ms on NTFS, 1.7 ms on ext4 and 33 ms on the NAS's btrfs. What it buys is
  the rename reaching the disk, not that the file system would have lost it
  without it: whether NTFS, ext4, btrfs or APFS keep the rename anyway is a
  power-cut test nobody has run.
- A writer killed between staging and renaming — power cut, `TerminateProcess`,
  `SIGKILL` — leaves one `<name>.<unique>.tmp` beside the file. The data file
  is untouched and still holds its previous value, and nothing removes the
  temporary on the write path: finding it means reading the directory, which
  would cost every write the directory's size. The name is the sweeper's
  handle — anything matching `<name>.*.tmp` beside the file is dead, because a
  live writer stages under the lock. Python's `sweep(path)` takes that lock and
  removes them, and returns how many; Go and C++ have no such call yet.
- A reader that polls is a tax on every writer of the file on Windows: 4.4 ms
  per update alone, 6 to 16 ms with one reader spinning, from open-close
  contention on the name and not from the rename retry.
- The names we derive are longer than the one you gave us — `<path>.lock` and
  `<path>.XXXXXXXX.tmp` — so on Windows with `LongPathsEnabled = 0` a path a
  bare `open()` accepts can be one `cas` cannot. **Python prefixes `\\?\` once
  the staged name would pass `MAX_PATH`, and Go's own `os` package does the
  same for it, so both take every path a bare `open()` takes and some it does
  not; C++ refuses from 246 characters up, where `open()` still works.**
- An edit that calls `write`, `change` or `sweep` on the file it is editing
  cannot be served: the lock is held and a second handle on it is another
  holder, so the call would wait for itself. **Python raises `Reentrant`; Go
  and C++ block forever with no message.** Nesting on a different file is fine
  and is how a record and its index are written together.
- An edit that returns no value where the file holds one is refused —
  `NoValue` in Python, `ErrNoValue` in Go — and the file is left as it was. It
  is not a way to empty a file (write an empty value) or to delete one (there
  is no delete). An edit that returns what it was given changes nothing, and on
  a missing file that means the file stays missing. C++ cannot express it: its
  `Edit` returns `std::string`.
- The lock file stays. Delete the data file and `<path>.lock` together.

## Conformance

[`mixed.py`](mixed.py): two Go, two C++ and two Python writers making 100
changes each to one file whose name is the last line of
[`names.list`](names.list) — an emoji, a CJK pair, a macron and a combining
acute, because until 2026-09-09 no test in any language used a file name longer
than one ASCII letter and the Windows rename in Python had been counting code
points where the kernel counts UTF-16 units — a reader per language spinning
through every rename —
[`CAS-MIXED1.txt`](https://github.com/openabstractions/abstractions/blob/main/docs/results/CAS-MIXED1.txt).
Windows; macOS `UNPROVEN`. Each language's own tests are named above.

## Where it sits

Below: nothing of ours. Above:
[abstraction-job](https://github.com/openabstractions/abstraction-job) writes
every record through it, and
[abstraction-config](https://github.com/openabstractions/abstraction-config),
[abstraction-asks](https://github.com/openabstractions/abstraction-asks) and
[abstraction-rights](https://github.com/openabstractions/abstraction-rights)
keep their files with it.

One layer of [openabstractions](https://github.com/openabstractions/abstractions).
Every layer names one thing local tools rebuild on their own; the name means the
same in each language that implements it, and the conformance scenarios are what
hold an implementation to it.

## Requirements

Go 1.25 or newer, standard library only. Python 3.9 or newer, standard library
only. C++17.

## Licence

Apache-2.0. See [LICENSE](https://github.com/openabstractions/abstraction-cas/blob/main/LICENSE).
