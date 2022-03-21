# dsfs

An experiment with Filesystem in USErspace (FUSE) with Discord attachments.

> :warning: **Use at your own risk!** This is an unfinished project and only for research or recreational purposes only.

### Introduction

Files are backed on Discord with a very primitive append-only filesystem.

There are two transaction types:

* WriteTx: Writes a file or folder
* DeleteTx: Deletes a file or folder

Some operations are implemented (sometimes efficiently) with combinations of
the two transaction types.

Opening a file will cache the entire file in memory. This is to greatly improve
the read and write latency at the cost of memory usage and long initial opening
times. Preferably, files should be cached on demand but this can be
surprisingly difficult to implement.

Writes are buffered and not flushed until the file is closed. Files that are
opened and not written to will not be flushed when closed.

### Neat features

* A full history of the filesystem is available by replaying transactions.
* There is technically no file storage limits.
* Transactions are stored in memory via a radix tree for fast path lookups.

### Limitations

* Some file explorers probe files by opening them to look for thumbnails, etc.
  This can cause files to load into memory. This can be prevented by moving
  large files into their own individual folders.
* Renaming folders is somewhat bugged. Since paths are hard coded in
  transactions, there is currently no good way to efficiently rename the path
  of child files/folders. We can experiment with path IDs, but it will greatly
  increase the complexity of the data structures.
* Some file attributes/modes are not implemented as it would be very complex to
  handle. This is an area that can be improved in the future.
