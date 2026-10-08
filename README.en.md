# silo

> Use S3 / MinIO like a Linux terminal — an interactive object-storage client written in Go.

`silo` maps object storage onto a virtual filesystem tree, so you can browse and
manage buckets and objects with familiar commands: `ls`, `cd`, `cp`, `mv`, `rm`,
`cat`, and more. It reads the same config file as
[MinIO mc](https://github.com/minio/mc) (`~/.mc/config.json`), so **existing
aliases work without reconfiguration**.

Built on the [minio-go](https://github.com/minio/minio-go) SDK. Pure Go,
**no `mc` binary required**, cross-compiles to Windows / Linux / macOS.

**中文** → [README.md](README.md)

> 🤖 **This project is developed entirely by AI; humans make the decisions.**
> Requirements, architecture trade-offs, and releases are decided by a human.
> Code, tests, and docs are written by AI.

## One-command install

```bash
# Linux / macOS (no Go required)
curl -fsSL https://raw.githubusercontent.com/nonex-code/silo/main/install.sh | sh

# Windows PowerShell
irm https://raw.githubusercontent.com/nonex-code/silo/main/install.ps1 | iex

# With Go (any platform)
go install github.com/nonex-code/silo@latest
```

---

## Contents

- [Features](#features)
- [Quick start](#quick-start)
- [Command reference](#command-reference)
- [Virtual filesystem](#virtual-filesystem)
- [Transfers and resume](#transfers-and-resume)
- [Config compatibility](#config-compatibility)
- [CLI flags](#cli-flags)
- [Scripting: humans vs agents](#scripting-humans-vs-agents)
- [Interactive keys](#interactive-keys)
- [FAQ](#faq)
- [Development](#development)
- [Layout](#layout)
- [License](#license)

## Features

- 🗂️ **Virtual filesystem** — `/` lists aliases, `/<alias>` lists buckets,
  `/<alias>/<bucket>/<key>` reaches objects. `cd` / `pwd` / `..` / `~` / `cd -`
  all work.
- 🔀 **Cross-alias operations** — `cp` / `mv` move data between aliases (even
  different providers): server-side `CopyObject` within one alias, download +
  upload through a temporary file across aliases.
- 🚀 **Reliable transfers** — concurrent multi-file transfers with per-file
  progress; automatic multipart upload above 32 MiB; resumable uploads
  (`<file>.silo-upload.json`) and downloads (HTTP Range); tunable concurrency
  and part size.
- ⌨️ **Comfortable interactive use** — Tab completion for commands, aliases,
  buckets, prefixes and objects; persistent history (`~/.silo_history`); colour
  output with `--no-color`.
- 🧩 **mc config compatible** — shares `config.json`, preserves unknown fields,
  `0600` permissions, atomic writes.
- 🖥️ **Cross-platform** — Windows / Linux / macOS, no cgo.
- 🤖 **Scriptable** — `-c "command"` or multi-line commands on stdin for CI.
- 📦 **Machine-readable** — `--json` emits one JSON object per command.

## Quick start

```bash
$ silo
silo 0.1.0  (type 'help' for commands, 'exit' to quit)
config: C:\Users\you\.mc\config.json

/ > ls /                        # list every alias
/ > use rufs                    # enter an alias (same as cd /rufs)
/rufs > ls                      # list buckets in the alias
/rufs > cd my-bucket/logs
/rufs/my-bucket/logs > ls -l
/rufs/my-bucket/logs > cat app.log
/rufs/my-bucket/logs > get app.log ./app.log          # download
/rufs/my-bucket/logs > put ./new.log .                # upload
/rufs/my-bucket/logs > cp app.log ooo/backup-bucket/  # cross-alias copy
/rufs/my-bucket/logs > mv old.log old-2025.log        # rename within an alias
/rufs/my-bucket/logs > rm -r tmp/                     # recursive delete
```

Add an alias (writes `~/.mc/config.json`, exactly like `mc`):

```bash
/ > alias set play https://play.min.io ACCESS_KEY SECRET_KEY
/ > alias set minio http://127.0.0.1:9000 minioadmin minioadmin --path on
/ > alias list
/ > alias remove play
```

## Command reference

Shared transfer flags: `-r/--recursive`, `-c/--continue`,
`-n/--no-clobber`, `--parallel N` (default 4).

### Navigation

| Command | Description |
| --- | --- |
| `ls [-l] [-R] [-a] [path...]` | List aliases / buckets / prefixes / objects (alias `dir`) |
| `ll [-R] [-a] [path...]` | Long listing: one entry per line with type, size, modified time, ETag, storage class |
| `cd [path]` | Change directory; no argument → `/`, `cd -` → previous directory |
| `pwd` | Print the current virtual directory |
| `tree [-L depth] [path]` | Recursive tree view |
| `use <alias>` | Enter an alias, same as `cd /<alias>` |

> `ls`/`ll` hide dot-prefixed objects by default; `-a` shows them. `cd` allows
> entering a not-yet-existing prefix (buckets are still validated), so you can
> `cd newprefix` and then `mkdir`/`put`.

### Objects

| Command | Description |
| --- | --- |
| `cat <file...>` | Print object contents |
| `head [-n N] <file...>` | Print the first N lines |
| `tail [-n N] <file...>` | Print the last N lines |
| `wc [-l] [-w] [-c] <file...>` | Count lines / words / bytes |
| `stat <path>` | Object or prefix details (size, ETag, storage class, version, owner…) |
| `du [-a] [path...]` | Recursive size (`-a` lists every object) |
| `find [path] [-name P] [-type f\|d] [-size +N\|-N] [-maxdepth N]` | Recursive search |
| `touch <file...>` | Create an empty object (or refresh an existing one) |
| `mkdir [-p] <prefix...>` | Create a prefix (`-p` creates parents); at an alias root it creates a bucket |
| `rmdir [-p] <prefix...>` | Delete an **empty** prefix (`-p` also removes empty parents) |
| `rm [-r] [-f] [-n] <path...>` | Delete objects / prefixes / buckets (`-n` dry run; aliases `delete`/`del`/`remove`) |
| `which <path>` | Resolve a path and report object / directory / bucket / alias |
| `grep [-i] [-v] [-n] [-l] [-c] [-E] [--include G] [--exclude G] <pattern> <path...>` | Stream-search object contents; prefixes recurse automatically |
| `hash [--type md5\|sha1\|sha256] <file...>` | Checksums (shortcuts `md5` / `sha1` / `sha256`) |
| `presign [-e 7d] <file>` | Generate a presigned download URL |

### Transfers

| Command | Description |
| --- | --- |
| `put [-r] [-c] [-n] [--parallel N] <local...> [remote]` | Upload files or directories (alias `upload`) |
| `get [-r] [-c] [-n] [--parallel N] <remote...> [local]` | Download objects or prefixes (alias `download`) |
| `cp [-r] [--parallel N] <source...> <target>` | Copy, cross-alias supported (alias `copy`) |
| `mv [-r] [--parallel N] <source...> <target>` | Move (copy then delete source; aliases `move`/`rename`) |
| `mirror [--overwrite] [--remove] [-n] [--include G] [--exclude G] [--parallel N] <source> <target>` | Incremental sync between a local directory and a remote prefix (`-n` dry run) |
| `diff [--include G] [--exclude G] <source> <target>` | Compare two trees (local or remote) and list differences |

> For `mirror`, local vs remote is decided by the first path segment: a path
> starting with a configured alias name (or `/alias/...`) is remote, anything
> else is local. By default only missing or size-changed objects are copied;
> `--overwrite` forces uploads and `--remove` deletes target-only objects.

### Buckets and config

| Command | Description |
| --- | --- |
| `mb <bucket...>` | Create one or more buckets |
| `rb [-f] <bucket...>` | Remove buckets (`-f` empties first) |
| `buckets [-a] [alias]` | List buckets; `-a` lists buckets of every alias |
| `alias [list \| set ... \| remove ...]` | Manage mc aliases (alias `aliases`) |
| `config` | Show the config file path in use |

### Other

| Command | Description |
| --- | --- |
| `help [command]` | Command list, or usage for one command (alias table appended) |
| `history [-c]` | Show (`-c` clears) this session's history |
| `info` | Version, config file, current directory, part size, concurrency |
| `clear` | Clear the screen |
| `echo <text...>` | Echo text (debugging) |
| `version` | Print the version |
| `exit` | Quit (aliases `quit` / `q`, or `Ctrl-D`) |

### Aliases

For mc / Unix muscle memory (`help` prints this table too):

| Alias | Equivalent |
| --- | --- |
| `dir` / `list` | `ls` |
| `copy` | `cp` |
| `move` / `rename` | `mv` |
| `upload` | `put` |
| `download` | `get` |
| `remove` / `delete` / `del` | `rm` |
| `aliases` | `alias` |
| `quit` / `q` | `exit` |

## Virtual filesystem

A path has three parts: `alias / bucket / key`.

```
/                              → all aliases
/rufs                          → all buckets in rufs
/rufs/my-bucket                → top-level prefixes and objects
/rufs/my-bucket/logs/app.log   → one object
```

- Relative paths resolve against the current directory and support `.`, `..`, `~`.
- `ls` shows S3 common prefixes as directories (trailing `/`), so `cd` works
  like a local shell.
- S3 has no real directories: `mkdir` writes a zero-byte `prefix/` marker.
- Cross-alias `cp`/`mv` downloads to a temporary file and uploads to the target
  alias, so large files still get multipart and resume; within one alias it
  calls server-side `CopyObject`.

## Transfers and resume

- **Single vs multipart**: objects ≤ 32 MiB use a single PUT; larger files use
  multipart upload.
- **Upload resume**: during multipart upload the `UploadID`, part size, and
  source file size/modtime go into `<local-file>.silo-upload.json`. Running
  `put -c` again queries the parts the server already has, skips them, and
  removes the state file on success.
- **Download resume**: `get -c` calls `StatObject`, compares the remote size
  with the local file, and appends the rest via HTTP `Range`.
- `--part-size 64M` and `--parallel 8` tune part size and concurrency.
- For S3 implementations without aws-chunked streaming signatures, multipart
  parts and zero-byte objects use explicit `Content-Length` / SHA256.

## Config compatibility

`silo` and `mc` share one config file. Lookup order (**existing files are
auto-detected when `MC_CONFIG_DIR` is unset**):

1. `$MC_CONFIG_DIR/config.json` (if set)
2. `~/.mc/config.json` (official mc default)
3. `~/mc/config.json` (some mc distributions / derivatives, e.g. certain RustFS tools)
4. `%APPDATA%\mc\config.json`, `%LOCALAPPDATA%\mc\config.json` (Windows)

If none exist it falls back to `~/.mc/config.json` so new aliases can be
created. Use `--config <path>` to override; the `config` command prints the
file actually in use.

Supported alias fields: `url`, `accessKey`, `secretKey`, `api`
(`S3v4` / `S3v2`), `path` / `lookup` (`auto` / `on` / `off` / `dns` for
path-style / virtual-host-style), `region`.

Writes are safe to share with `mc`:

- **Unknown fields preserved** — unknown top-level keys and unknown keys inside
  untouched aliases are kept as-is.
- **Atomic writes** — temp file then replace, mode `0600`.
- **Tolerant reads** — `version` may be a string (`"10"`) or a number (`10`);
  UTF-8 BOM tolerated.
- **Env aliases** — mc's `MC_HOST_<name>` variables are supported (read-only,
  never written back); `alias list` shows `env` in the `SOURCE` column.

### MC_HOST_* environment aliases

Same as mc, define an alias with an environment variable:

```bash
# MC_HOST_<name>=scheme://ACCESS_KEY:SECRET_KEY@HOST[:PORT]
export MC_HOST_play='https://ACCESS_KEY:SECRET_KEY@play.min.io'
silo -c "ls /"        # play is listed too
```

Environment aliases take precedence over same-named file aliases. Explicitly
re-setting one with `alias set` makes it persistent in the file.

## CLI flags

```
silo [flags] [command ...]

  --config string     path to mc config.json (auto-detected by default)
  --alias string      enter this alias on startup
  -c string           run one command and exit (also accepts a trailing command)
  --no-color          disable coloured output
  --json              emit one JSON object per command (machine readable)
  --parallel int      default transfer concurrency (default 4)
  --part-size string  multipart part size with K/M/G suffix, e.g. 16M, 64M (default 16M)
  --history string    history file path (default ~/.silo_history, or SILO_HISTORY)
  --version           print version and exit
  -h, --help          show help
```

## Scripting: humans vs agents

There are exactly **two intended usage modes**, and they share one grammar and
one execution path (`Eval`):

| Who | How | When |
| --- | --- | --- |
| Human | `silo` | Interactive prompt, Tab completion, history |
| Agent | `silo -c "<one command>"` | Scripts / CI, add `--json` for machine output |
| Batch | `printf 'cd /a\nls\n' \| silo` | Multiple lines in one process (state kept) |

- Trailing arguments (`silo ls -l`) and stdin both route through the **same**
  `Eval`, so no second command language exists.
- `--json` / `--no-color` / progress bars are output-layer switches applied
  once at the `Eval` boundary; commands never know who is calling.

```bash
silo -c "ls /"
silo --config ./config.json --alias minio -c "find t/bucket -name '*.log'"
printf 'use minio\nls\n' | silo        # read multi-line commands from stdin

# Machine readable: --json wraps the same command in one JSON line
silo --json -c "ls /minio/bucket"
# {"command":"ls /minio/bucket","ok":true,"exit_code":0,"stdout":"a.txt\nb.txt\n"}
printf 'ls /\necho hi\n' | silo --json   # one JSON object per line (NDJSON)
```

**Exit codes** (same as the process status): `0` on success; `1` when a command
fails, including partial failure across multiple targets; `1` when `grep` finds
no match (like Unix `grep`). In `--json`, `exit_code` matches the process status;
a non-empty `stderr` means error output, and command errors also appear in the
`error` field.

With `-c`, use **absolute paths** (`/alias/bucket/key`): the working directory
does not persist across processes.

## Interactive keys

- **Tab**: complete command names, aliases, buckets, prefixes, objects, and each
  command's flags (type `-` first). `put` / `mirror` / `diff` complete local and
  remote paths together.
- **Up/Down**: browse history.
- **Ctrl-C**: cancel the current line without quitting.
- **Ctrl-D**: quit.
- History lives in `~/.silo_history`; `history -c` clears memory and file.

## FAQ

**Q: My existing mc aliases are not found.**
Run `config` to see the file silo uses. silo auto-detects `~/.mc`, `~/mc`, and
Windows `%APPDATA%\mc` / `%LOCALAPPDATA%\mc`. If mc uses another directory, set
`MC_CONFIG_DIR` or pass `--config <path>`.

**Q: Do I need `mc` installed?**
No. silo only reads mc's config format; all network calls use the Go SDK.

**Q: Does it support S3-compatible services other than AWS S3?**
Yes — MinIO, Ceph, Alibaba OSS, Tencent COS, Cloudflare R2, etc. Use
`alias set ... --path on` for path-style access.

**Q: Why is `-r` required when uploading a directory?**
Like `cp`/`rm`, recursive operations must be explicit to avoid uploading a whole
tree by accident.

**Q: Can I delete the upload resume state file?**
Yes. The next upload starts from scratch; a successful upload removes it
automatically.

**Q: Can I use pipes and redirection?**
There is no `|` or `>` syntax. Combine with an external shell instead, e.g.
`silo -c "cat t/bucket/a.log" > a.log`.

## Development

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./...
make release   # cross-compile every platform into dist/
```

Integration tests start an in-process S3-compatible server via
[gofakes3](https://github.com/johannesboyne/gofakes3) and cover the virtual
filesystem, upload/download, cross-alias copy and move, multipart upload,
resume, directory de-duplication, and Tab completion — no real server needed.

Releases are built and published by GitHub Actions when a version tag is pushed:

```bash
git tag v0.2.0 && git push origin v0.2.0
```

## Layout

```
main.go                      CLI entry: flags, signals, REPL startup
internal/config/             mc config.json read/write (compatible, lossless)
internal/s3fs/               S3 client, listing, copy, multipart & resume
internal/shell/              VFS paths, commands, completion, history, REPL
internal/ui/                 Colour output and progress bars
```

## License

[MIT](LICENSE)