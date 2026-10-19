# abstraction.cas contract

Binds: `cas.thrift`

`cas.thrift` names `abstraction.cas/store@1`, a direct language interface
with no endpoint. This page holds the rules for where the CAS lock lives:
which spellings of one file share it, and how far it reaches. The other rules
implementations sharing a file agree on are in the README's
[What a fourth implementation must do](README.md#what-a-fourth-implementation-must-do).

## Reading this page

The key words "MUST", "MUST NOT", "SHOULD" and "MAY" in this page are to be
interpreted as described in RFC 2119 and RFC 8174, when, and only when, they
appear in all capitals, as shown here.

## Ownership and compatibility

TD3's 0.8 target is one Go writer inside services/providers. Generated language
clients call the service owning shared state; a shared C++ client transport does
not require a C++ CAS implementation. CAS remains an internal direct primitive,
not a new public service. The source architecture's small-state ownership row
links the current TD3 disposition and its service-first authority.

The standalone C++ compatibility source retains its existing API and package;
it is omitted from the aggregate root SDK. The unpublished ABI1/control surface
is withdrawn. A-LANDED records this completed selective source retirement
(`backlog/history/A-LANDED.md`). Python's payload remains in its pinned adopter
archives. Old Python/C++ v0.1.0 tags
remain immutable. These retained sources do not change the Go-writer target or
introduce a Go native-runtime dependency. The shared-file rules below continue
to govern retained compatibility implementations.

## Divergences

- **`Read(path)`.** The path is an explicit local-binding extension: it names
  a file on the provider's machine and is meaningful only there. CAS is a
  direct provider utility with no IPC endpoint. Applications use the service
  owning their state; a serialized path supplies no filesystem authority.

## Lock

**[CAS-L1] Every spelling of one file takes one lock.** Writers that name
one data file through different spellings MUST exclude each other. The lock
is keyed on the parent folder plus the leaf, never on the data file's own
identity, which every write replaces by renaming a new file over it. An
implementation passes the original spelling to the file identity owner and
names the data file, its lock and its staging directory from the leaf and
folder paths that owner gives it (`fileid.ResolveLeaf`, ID-F7). The owner
walks `..` physically on POSIX and preserves verbatim Windows names. Its
answer names the folder at its final path after
junctions, symbolic links and 8.3 aliases, and the leaf as the folder holds
it, an 8.3 alias in its long form. The beside lock is that spelling plus
`.lock`. A placement locates the file below its root by file identity, so
every spelling below root names one `<side>/<rel>.lock` and one target
`<root>/<rel>`. The file's own name is read as written apart from an 8.3
alias: a symbolic or hard link at that name is replaced by the rename, and
its target keeps its value. An 8.3-shaped name of a file that does not exist
yet stays as written.

Before this rule the lock was the given spelling plus `.lock`. Writers through
`counter-record.json` and its alias `COUNTE~1.JSO` kept 160 of 320 updates on
Windows, and writers through `root/counter-record` and
`root/link/../counter-record`, where the kernel reads `link/..` from the
link's target, kept 171 of 320 on Linux. `TestTwoSpellingsOfOneFileLoseNoUpdate`
in `go/spelling_test.go` runs both, an 8.3 alias of the folder, a junction on
Windows, a symbolic link to the folder on Linux and macOS, and a plain `..`,
beside the file and through a placement.

**[CAS-L2] The lock holds within one host.** A byte-range lock and a `flock`
exclude only writers on the host that holds the volume. Writers on a PC over
SMB and writers on the NAS's own volume each took the lock while the other
held it and lost or refused 147 to 149 of 2150 updates in three runs; six
writers on one host lost none of 1800 (2026-09-08, the README's "The lock is
one machine's"). [`Write`](https://openabstractions.github.io/reference.html#concept-cas.write-file), [`Change`](https://openabstractions.github.io/reference.html#concept-cas.change) and `Sweep`, and a placement's root and
side, MUST refuse a path that `identity.LocalPath` refuses, before opening
anything there, with the reason it gives. On Windows it reads the spelling
and the type of the drive the spelling names: a UNC path, a `\\.\` path and
a network drive are refused. A folder link on a local drive whose target is
a share passes that check, and writers through it lock across hosts. On
Linux a network file system and a FUSE mount of unknown type are refused, on
macOS a volume without `MNT_LOCAL`, and on both a volume that does not
report its type within identity's deadline of 500 ms; such a write is
refused where it used to wait. In WSL a Windows drive under `/mnt` is a 9P
file system and is refused: its Windows writers lock through another kernel.
In Go `cas.LocalPath` gives the same answer for a store's root, and the error
matches `errors.Is(err, identity.ErrNotLocalPath)`. [`Read`](https://openabstractions.github.io/reference.html#concept-cas.read-file) takes no lock and
reads any path. Writers on two hosts change one record through the owning
service's API with a revision precondition, as config's base and revision
and rights' expected revision do. `TestAFileOnANetworkShareIsRefused` in
`go/share_test.go` refuses `\\localhost\C$` on Windows and a volume
identity treats as a network one on Linux and macOS, and leaves the folder
empty.

Two kinds of store meet this refusal. A `job` file store refuses its root
when it opens (`job.NewFileStore`), and the `nas` download delegate refuses
a store on a share. The runtime obtains `nas_store` and the run overrides
`ABSTRACTION_NAS_STORE` or `--jobs-download-nas-root` through the settings
service and prepares the NAS provider in its executor lifetime. Until
that read and preparation succeed, new NAS admission is unavailable;
an unavailable or refused setting creates no provider pin or delegated
write. Accepted NAS work retains its binding and waits while independent
runtime services keep serving. The one-host refusal precedes any new
provider pin or delegated write. The PC and the NAS both wrote
that store's records, which is the two-host case measured above. A `jobd` on
the NAS keeps its store on its own volume. State kept under the roaming
profile (`os.UserConfigDir`: config's user file, asks, rights, download's
refusals, the Panel's setup records and the gateway's handles) is refused
when Windows redirects `AppData\Roaming` to a share or a network drive.

## Conformance

Go meets CAS-L1 and CAS-L2. The Python and C++ writers name the lock from
the spelling they are given and open a path on a share. Whether they keep a
writer is an open decision.
