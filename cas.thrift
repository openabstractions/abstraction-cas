namespace * abstraction.cas.api

// Direct compare-and-set API; generated with --no-ipc. No service is implied.
encoding json {
 escape="minimal"
 indent="2"
 map_keys="utf8-bytes"
 numbers="integer-decimal"
 opaque="verbatim"
 terminator="newline"
 duplicate_keys="refuse"
 depth_limit="64"
}
refusal {
 1: malformed(stage="grammar")
 2: bad_string(stage="grammar")
 3: number_spelling(stage="grammar")
 4: wrong_type(stage="grammar")
 5: bad_binary(stage="grammar")
 6: depth_exceeded(stage="grammar")
 7: duplicate_key(stage="grammar")
 8: duplicate_field(stage="structure")
 9: unknown_field(stage="structure")
 10: missing_field(stage="structure")
 11: trailing_bytes(stage="document")
}
// Absent data means no file. Present empty data means an existing empty file.
// Every octet is valid data, including NUL and bytes that are not UTF-8.
struct Value {
 1: optional binary data(omit="absent")
}(document="true",unknown_fields="refuse")
service Store {
 Value Read(1:string path)(doc="Read the current bytes. Absent data means the file does not exist; present empty data means an empty file. Filesystem failures remain implementation errors.")
 void Write(1:string path, 2:Value base, 3:binary data)(doc="Replace only if current value equals base, including presence. A mismatch is Moved. A successful write holds the kernel lock through staging, synchronization and replacement. Missing output data is not an empty write. Existing provider errors remain errors; Change callbacks remain local conveniences.")
}(wire_name="abstraction.cas/store@1",doc="Compare-and-set over a file. This descriptor is emitted as a direct language interface with no IPC. Native providers own locking and replacement. The wire_name is schema metadata, not an available service endpoint.")
