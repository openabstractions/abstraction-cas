package cas

// SyncDir flushes the metadata of an existing directory after an entry is
// replaced or removed. It opens and closes its own directory handle.
//
// A filesystem that explicitly cannot flush directories leaves the change
// without a durability guarantee and returns nil: EINVAL or ENOTSUP on POSIX;
// ERROR_INVALID_FUNCTION, ERROR_NOT_SUPPORTED or ERROR_INVALID_PARAMETER on
// Windows. Open, metadata, close and all other flush errors are returned.
func SyncDir(dir string) error { return syncDir(dir) }
