# dsfs

An experiment with Filesystem in USErspace (FUSE) with Discord attachments.

> :warning: **Use at your own risk!** This is an unfinished project and only
> for research or recreational purposes only.

## Introduction

Files are backed on Discord with a very primitive append-only filesystem.

This is considered pre-alpha software and there will be bugs pertaining to
synchronization and functionality.

## How it works

### Format

There are two channels:

* \#tx: stores transaction data
* \#data: stores file data in chunks

Tx format:

* tx: 0 for write, 1 for delete
* type: 0 for file, 1 for folder
* mtim: modification time
* ctim: creation time

```json
{
  "tx": 0,
  "type": 0,
  "path": "/test.mp4",
  "ids": [
    "956040610224148480",
    "956040626284146698",
    "956040643749224478",
    "..."
  ],
  "sums": [
    "Y9Whjuk_kbopDYx7cdSHXrzApvk=",
    "sxFp01p0Q52hF-q8LWGi1DoXX-M=",
    "7PljyMpvTDE-cfaZtm532OTwG7U=",
    "..."
  ],
  "mtim": "2022-03-22T23:01:08.3596736-04:00",
  "ctim": "2022-03-22T23:01:07.9266501-04:00",
  "size": 348437445
}
```

Some transactions use a combination of the write and delete transactions (e.g. rename).

### Walkthrough

On startup, all transactions are downloaded from the #tx channel and applied in
sequential order in a radix tree structure.

Radix trees have fast prefix lookup which is beneficial for querying file and
folder paths.

When a folder is opened, the path is queried in the radix tree and the
immediate children (files and folders) are listed along with basic metadata
such as size, modification/creation times, etc.

Opening a file immediately starts a background process that will load the file
into memory. The first and last pieces (same behavior as BitTorrent) are
downloaded first so that certain applications are able to preview the file.

Reading the file will incur almost no performance penalty as the file is fully
buffered in memory. In the future, this would preferably be changed so that the
file is streamed with only part of the file buffered, but it may cause large
amounts of latency.

Writing to the file happens in memory. The changes are not immediately
reflected on the remote filesystem as writes often happen in small chunks.

Each opened file has a dirty bit associated with it which is set to true when
the file is modified in memory. Upon closing the file, the dirty bit is checked
and file data is uploaded. Checksums are used to ensure that there are no
unnecessary chunks uploads.

Syncing between clients occurs when a client sends a transaction in the #tx
channel. Another client picks up the transaction and applies it on their
filesystem. When a file is already opened, checksums are used to ensure that
only modified chunks are downloaded and patched.

Since the filesystem is append-only, each historic state of the filesystem is
saved and can be recovered in the future by replaying transactions up to a
specific date and time.

## Areas of improvement

Renaming folders is horribly inefficient as it involves renaming all children.
This issue can be solved by giving each file/folder a permanent id and create a
mapping between paths and ids.

When performing many operations at once, you can get ratelimited. This issue
can be alleviated by running a connection pool to artificially increase the
ratelimits.

A lot of permissions and attributes are not implemented. In most systems, the
size attribute is all that matters. Some attributes such as access times is
unfeasible to implement as the attribute would be stale most of the time.

Startup times can get long when there is a long transaction history. To speed
things up it is possible to create periodic snapshots where multiple transactions
are put into one transaction message. Each message can hold 8MB of transaction data
which can manage upwards of terabytes of data.
