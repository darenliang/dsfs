# dsfs

An experiment with Filesystem in USErspace (FUSE) with Discord attachments.

> :warning: **Use at your own risk!** This is an unfinished project and only
> for research or recreational purposes only.

Files are backed on Discord with a very primitive append-only filesystem.

This is considered pre-alpha software and there will be bugs pertaining to
synchronization and functionality.

Here is a blog post going over some of the implementation
details: https://www.darenliang.com/posts/fuseing-for-fun

## Cross-platform support

[Cgofuse](https://github.com/winfsp/cgofuse) is used which supports Windows,
macOS and Linux.

The FUSE libraries required for each platform:

* Windows: [WinFsp](https://github.com/winfsp/winfsp)
* macOS: [macFUSE](https://osxfuse.github.io/)
* Linux: [libfuse](https://github.com/libfuse/libfuse)

## Usage

Please check the requirements for building
with [cgofuse](https://github.com/winfsp/cgofuse).

To build:

```bash
go build
```

To run:

```bash
dsfs -t <Bot Token> -s <Server ID>
```

To run with transaction compaction:

```bash
dsfs -t <Bot Token> -s <Server ID> -c
```
