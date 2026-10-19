# abstraction-cas

*For provider authors. An application uses the capability service a
provider composes CAS into.*

CAS (compare-and-set) keeps one small shared state file correct when several
processes or programming languages update it. A killed writer leaves the
previous complete value, and two writers cannot silently overwrite each other's
changes. [`Read`](https://openabstractions.github.io/reference.html#concept-cas.read-file) returns the whole value, [`Write`](https://openabstractions.github.io/reference.html#concept-cas.write-file) replaces the value only when it
still matches, and [`Change`](https://openabstractions.github.io/reference.html#concept-cas.change) edits under the shared operating-system lock.

## Ownership and compatibility

The 0.8 target is one Go CAS writer inside services and providers (TD3).
Applications use generated clients of the service that owns their state; they
do not open that service's files. CAS itself adds no daemon or public endpoint.
The [contract](CONTRACT.md#ownership-and-compatibility) distinguishes this target
from retained compatibility source.

The C++ standalone package and its existing API remain available for historical
compatibility; CAS is omitted from the aggregate root C++ SDK. The unpublished
C ABI1 and migration-only control API were withdrawn before publication. Go's
behavior, CGO-disabled builds and Go 1.25 floor remain unchanged, with no new
native runtime dependency. A-LANDED records the completed selective source
retirement (`backlog/history/A-LANDED.md`). Python source still travels in pinned
adopter archives. Its presence is not a new Python-writer adoption path.

## The problem

Every service that keeps state in a file rewrites it, and two writers on one
file lose each other's updates unless they agree on a lock and a rename. Each
service of ours hand-rolled its own writer. This is the floor under all of them:
one lock, one rename, one Go writer owner for service state. It makes no
capability claim; [CONTRACT.md](CONTRACT.md) holds the rules for where its
lock lives. A stranger implementing `job` needs its page, not this.

The example is in [Example](#example).

The Go implementation is a direct provider utility with no discoverable service
contract. The Python and C++ material below documents retained compatibility.

## Words

| word | meaning |
|---|---|
| **read** | the whole file, or nothing when there is none |
| **write** | replace the file only if it still holds `base`; else `Moved` |
| **change** | read, edit and write under the lock, so an edit always sees the truth and never needs a retry |
| **base** | the value a writer read; `nil` means *only if the file does not exist* |
| **lock** | byte 0 of `<path>.lock`, where `<path>` is the file's one spelling ([CAS-L1](CONTRACT.md#lock)), exclusive, blocking, released by the kernel when the holder dies; it holds within one host ([CAS-L2](CONTRACT.md#lock)) |
| **placement** | a root and a side directory on its volume; for `<root>/<rel>` the lock is `<side>/<rel>.lock` and the replacement is staged in `<side>` |

No rule on this page carries a tag. The invariants below each carry a test, and
*What a fourth implementation must do* is cited by
[the job page](https://github.com/openabstractions/abstraction-job).

## Obtain

The primitive needs no runtime installation. Applications use their owning
service through the facade: https://openabstractions.org/adopt.html

- **Go.** `go get github.com/openabstractions/abstraction-cas/go`.
  [Releases, newest first](https://github.com/openabstractions/abstraction-cas/tags);
  pin the exact tag you tested against.
- **Python.** Not on any index —
  [what to install, import and call](python/README.md).
- **C++ compatibility.** Immutable `cpp/v0.1.0` remains available. Current
  standalone compatibility source uses C++17 and the FileID SDK prefix:
  `cmake -S cpp -B build -DCMAKE_PREFIX_PATH=<fileid-prefix>`, then
  `cmake --build build`. It exports `abstraction::cas` through its own
  `abstraction_cas` package, independently of the aggregate SDK.

Whether to adopt this at all, what it costs and what is not proven:
[Adopting](CONTRIBUTING.md#adopting).

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

A read on Windows retries an open that answers access denied or a sharing
violation, because a writer's replace gives those answers for an instant. A
file whose read is denied for good gives them on every try, and the retry ends
after 2000 tries, several seconds. A reader with a deadline passes it:
`ReadContext(ctx, path)` and `ReadLimitContext(ctx, path, max)` in Go return
`ErrRefused` (wrapping the context's error and the last denial) once `ctx` ends,
`read(path, timeout=s)` in Python raises `Refused`, and `read(path, deadline)`
in C++ throws `Refused`, a `std::system_error` carrying the last answer.

`Read` returns the whole file, or `nil` when there is none. `Write(path, base,
data)` replaces the file only if it still holds `base` (`nil`: only if it does
not exist), else `ErrMoved`. `Change` reads, edits and writes under the lock, so
an edit always sees the truth and never needs a retry.

Retained compatibility source exposes the same three calls in
`python/abstraction_cas.py` (`read`, `write`, `change`,
`None` for missing, `Moved`) and `cpp/include/abstraction/cas.h` (`std::nullopt`
for missing, `Moved` thrown, an edit refuses by throwing). All three share one
lock and one rename. The historical Windows receipts below retain their stated
revision and scope. Reproduce that compatibility scenario with
`CAS_CPP=<standalone-cas-build>/Release/test_cas.exe python tests/mixed.py 3`;
`scripts/results.sh` requires the same explicit standalone `CAS_CPP` selection
for `CAS-MIXED1`. It is outside the normal aggregate SDK check.

A directory that is read by listing it needs its data files alone. A placement
keeps the lock file and staged replacements in a side directory on the same
volume: `cas.NewPlacement(root, side)` in Go, `Placement(root, side)` in Python
and `abstraction::cas::Placement` in C++, each with the same `Read`, `Write` and
`Change` (Python adds `sweep`). For `<root>/a/b` the lock is `<side>/a/b.lock`,
and the replacement is staged as `<side>/a/b.<unique>.tmp`. The calls without a
placement are unchanged and keep both beside the file.

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
- **A placement keeps its root to data files.** Under a placement, root holds
  only the files written, through contention across processes and after a
  writer killed between staging and renaming (`go/placement_test.go`).

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
every PC writer dying within its first few changes. Six
writers in three languages on the PC alone, over the same share, lost nothing
in 1800. Two hosts on one record need a protocol this file does not have.
Go refuses a path on a share before opening anything there
([CAS-L2](CONTRACT.md#lock)); the Python and C++ writers still open it.

## What a fourth implementation must do

Two implementations on one file agree on the bytes they write and on the way
they keep each other out. Every single-language test proves the first only: a
writer with another lock passes all of them and loses updates against ours.

- **The lock is byte 0 of `<path>.lock`, exclusive, blocking, no timeout.**
  `<path>` is the one spelling [CAS-L1](CONTRACT.md#lock) names: absolute,
  cleaned, the parent folder at its final path and the leaf as the folder
  holds it, an 8.3 alias in its long form.
  A path [CAS-L2](CONTRACT.md#lock) refuses is refused before anything is
  opened. The lock file sits beside the data file, is opened read-write and created
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
  removed. POSIX is `rename(2)`. On macOS the staged file and, after the
  rename, the directory are flushed with `fcntl(F_FULLFSYNC)`, with plain
  `fsync` only where the volume answers `ENOTSUP`; the directory-flush bullet
  under *Today* gives the error set.
- **`Read` opens with every share mode on Windows** and holds the handle for
  the read only.
- **A placement moves the lock and the staging directory, and nothing else.**
  For `<root>/<rel>`, where `<rel>` is the path relative to root, the lock is
  byte 0 of `<side>/<rel>.lock` and the replacement is staged in the directory
  of `<side>/<rel>` under the same unique-name rule, then renamed over
  `<root>/<rel>`. Both directories are created when missing.
- **A placement refuses what its rename cannot serve.** A side directory that
  is root or contains it is refused. So is one on another volume than root:
  `st_dev` on POSIX, the volume serial number on Windows. A path that is root,
  lies outside it, or lies inside side is refused.
- **Every writer of one file uses the same placement.** `<path>.lock` and
  `<side>/<rel>.lock` are two locks that exclude nobody. `tests/mixed.py 3 --side`
  runs the six writers and three readers through one placement.

Prove it by being the seventh process in `tests/mixed.py`: a program that, given
`CAS_PATH`, `CAS_ROLE` and `CAS_N`, applies `CAS_N` increments of the `"n n"`
counter as a `writer` or reads until it sees `CAS_N` as a `reader`, aborting on
a torn read. Add its command to `commands()`; the run counts its updates with
ours. A fourth implementation whose lock ours cannot see loses a third of its
updates on the first run — measured with a Python writer on byte 1 on Windows
and on `lockf` on Linux, by that seventh process. `tests/mixed.py`
takes a directory as its second argument; a UNC path runs the six on a share.
Since 0.8.0 the Go writers refuse a UNC directory (CAS-L2), so a share run
measures only the C++ and Python writers.

## Today

Go, Python and C++, each the three calls. Consumed by `job`'s file store and
acceptance provider, `config`, `rights`, `asks`, `download`, `inference`,
the provider kit and the Lemonade and model-bridge providers, the runtime in
`serve`, the Panel's setup records and the MCP gateway.

What may break:

- A handle on the data file without delete sharing — an editor, `type`,
  Python's `open()` — makes a Windows replace fail with `ERROR_SHARING_VIOLATION`
  for as long as it is held. The writer retries 1000 times — 160 to 470 ms on
  local NTFS, process start included, the same in all three languages — then
  returns the error with nothing left behind; the lock was held throughout, so
  no other writer's update is lost, and the file still holds the previous
  value. A handle opened with `FILE_SHARE_DELETE` keeps reading the bytes it
  opened while the rename succeeds under it, 2 ms measured.
- Over SMB, measured before Go refused a share
  ([CAS-L2](CONTRACT.md#lock)); the Python and C++ figures stand. Every open
  handle on the data file blocks the replace, ours included, share-delete or not, from the same client or another process on
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
- Every write is an fsync: about 6 ms on Windows; on macOS, where both
  flushes are `F_FULLFSYNC`, about 8 ms (Go, 200 writes). Right for a policy file
  written at human rate; wrong for a lease renewed every millisecond.
- **Go flushes the directory after the rename on Windows and POSIX.** Its
  exported `SyncDir` is also the storage commit and credentials migration
  owner. Windows uses `FlushFileBuffers` on a directory handle with write-data
  access and backup semantics. `ERROR_INVALID_FUNCTION`, `ERROR_NOT_SUPPORTED`
  and `ERROR_INVALID_PARAMETER` acknowledge the change without a directory
  durability guarantee; other failures, including access denied, are returned.
  The retained Windows C++ compatibility writer still flushes only the file.
  Without a directory flush a power cut can lose the rename: the previous
  value survives, the acknowledged one may not. Go and C++ `fsync` the
  directory on POSIX and treat `EINVAL` and `ENOTSUP` as a file system that
  cannot, as Python does. On macOS every writer flushes the staged file and
  the directory with `fcntl(F_FULLFSYNC)`, because plain `fsync` leaves them in
  the drive cache; a volume that answers `ENOTSUP` gets plain `fsync`. Any
  other error on the staged file fails the write; on the directory `EINVAL`
  and `ENOTSUP` still count as a file system that cannot flush. Go's `Sync` falls back on `ENOTSUP` alone, so
  the other languages use that set too. Python opens the directory and flushes it — `fsync` on POSIX, `FlushFileBuffers` on a
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
  removes them, and returns how many. Go's `cas.Sweep(path)` and C++'s
  `abstraction::cas::sweep(path)` do the same. All three match only a unique
  part of letters, digits and underscores, so `<name>.lock.<unique>.tmp`, which
  belongs to the file `<name>.lock`, is left alone. Under
  a placement the temporary is `<side>/<rel>.<unique>.tmp`, root keeps only its
  data file, and each language's `Placement` sweep removes it.
- A placement compares volumes, not mounts. Two bind mounts of one Linux file
  system share `st_dev`, and a rename between them still fails with `EXDEV`:
  the write returns that error with the temporary removed and the file
  unchanged.
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
  blocks forever with no message.** Standalone C++ compatibility source refuses
  the nested mutation with `std::logic_error`. Nesting on a different file is fine
  and is how a record and its index are written together.
- An edit that returns no value where the file holds one is refused —
  `NoValue` in Python, `ErrNoValue` in Go — and the file is left as it was. It
  is not a way to empty a file (write an empty value) or to delete one (there
  is no delete). An edit that returns what it was given changes nothing, and on
  a missing file that means the file stays missing. C++ cannot express it: its
  `Edit` returns `std::string`.
- The lock file stays. Delete the data file and `<path>.lock` together; under a
  placement, the data file and `<side>/<rel>.lock`.

## Conformance

[`tests/mixed.py`](tests/mixed.py): two Go, two C++ and two Python writers making 100
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

## Status

**In development.** No capability claim, contract page or standalone
conformance scenario. Pin a reviewed entry from
[the tag list](https://github.com/openabstractions/abstraction-cas/tags).

## Licence

Apache-2.0. See [LICENSE](https://github.com/openabstractions/abstraction-cas/blob/main/LICENSE).

## Schema-defined direct API

`cas.thrift` defines Read and Write with `--no-ipc`: no daemon, client transport
or service endpoint is implied. Generated API reference is
[abstraction.cas.api.schema.html](abstraction.cas.api.schema.html).

Go callers can use `github.com/openabstractions/abstraction-cas/go/api`:
`var store api.Store = api.FileStore{}`. C++ callers include
`<abstraction/cas_api.h>` and use `abstraction::cas::FileStore` through
`abstraction::cas::api::Store`, linking the existing `abstraction::cas` target.
These adapters use the existing native provider and preserve its errors.
The original functions and callback-based Change remain available unchanged.

Read returns a Value: absent data means no file; present empty data means an
empty file. Data is arbitrary bytes, not UTF-8 text. Write compares presence
as well as bytes before replacement. Generated Go, C++ and Python descriptors
include canonical Base64 record codecs; codec availability does not imply IPC.
The Python descriptor is generated source under `py/`; it is not yet wired into
the existing Python package's installed public API.

For an installed C++ dependency, configure and build `cpp/`, then run
`cmake --install <build> --config Release --prefix <prefix>`. Consumers use
`find_package(abstraction_cas 0.8 CONFIG REQUIRED)` and link `abstraction::cas`.
The install includes both the native and generated headers. A standalone
consumer regression lives at `cpp/test/installed`: configure it with
`-DCMAKE_PREFIX_PATH=<prefix>`, build it, then run CTest in its build directory.

Write propagates native Go `ErrMoved` and C++ `Moved` on comparison mismatch.
The schema currently documents that error in prose: the generator does not yet
support declared `exception`/`throws` types. Its RPC `ServiceError` mechanism
is not emitted for this direct API and does not replace those native errors.

<!-- BEGIN GENERATED PERMISSION DEFINITIONS -->

## Permission definitions

Generated from the existing compiled catalogue. [RIGHTS.json](RIGHTS.json) contains the complete action/profile records, disclosure, lifecycle, first-use behavior, selector support and finite bundles.

This projection has no named action definition for CAS (compare-and-set). Atomic shared-state updates and the composing service's admission remain with their existing owners.

<!-- END GENERATED PERMISSION DEFINITIONS -->
