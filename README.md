# arc

A [restic](https://restic.net/) wrapper with directory-based configuration and strong client-side integrity protection.

A directory containing an `arc.conf` is a self-contained backup unit, similar to a git repo. The config file says how the directory is backed up, and `arc` keeps an `arc.sum` checksum manifest next to it to track what changes on disk between backups.

## Motivation
### Restic configuration
Most restic setups keep their configuration outside the data, in a shell script, a systemd unit, or a set of exported environment variables. `arc` keeps it in the directory being backed up. Running `arc restic backup` from anywhere inside the tree walks up to find `arc.conf`, the way `git` finds `.git`. The config moves with the directory, and two directories on the same machine can point at different repos with their own retention and excludes.

### Integrity checking
Restic verifies the integrity of its backup repos, but nothing verifies the files going into it. A file corrupted by a failing drive, bit rot, or accidental changes looks like an intentional edit and gets backed up as one.

For files accessed frequently this is not a big problem, since you would likely notice the corruption. It matters for archives: photos, documents, old projects. Corruption there can develop and worsen unnoticed for years, and by the time it turns up the last good copy can be difficult to find or pruned and lost forever.

`arc check` efficiently hashes every file and reports what was added, moved, removed, and modified since the last run. Unexpected changes to files can thus be caught locally before the next backup, so the corrupted version never makes it into your restic repo and you can easily restore the correct version from your latest restic snapshot.

## Install

Requires the [Go toolchain](https://go.dev/dl/) and `restic` on your `PATH` if you are going to use the restic wrapper functionality.

```sh
git clone https://github.com/srhickma/arc && cd arc
./install.sh
```

This builds `arc` and installs it to `/usr/local/bin`.

## Quick start

```sh
cd /data/archive
arc init                       # write a starter arc.conf
$EDITOR arc.conf               # set desired configuration

arc check                      # record checksums for all files
arc restic backup              # back up using the default profile
```

From then on, the routine is:
```sh
arc check                      # review what changed locally
arc restic backup              # if the diff looks right, back it up
```

Any command can be run from a subdirectory, or from anywhere with `--dir`:

```sh
arc --dir /data/archive check
```

## Commands

### `arc init`
Initializes the target directory with a starter config.

### `arc check [--deep]`
Walks the directory, computes checksums, and diffs them against `arc.sum`:
```
starting shallow check (modified files) ...
finished checking 4212 file(s) in 1.204s; 7 checksum(s) calculated over 84.31MB
   ADDED: /data/archive/2026/img-4417.jpg
   MOVED: /data/archive/inbox/scan.pdf -> /data/archive/docs/scan-xyz.pdf
 REMOVED: /data/archive/tmp/thumb.png
MODIFIED: /data/archive/2014/img-0132.jpg - a3f1... -> 91cd...
1 added, 1 moved, 1 removed, 1 modified
write new checksums to arc.sum? [y/N]:
```

A file that moved without changing content is reported as a move rather than an add plus a removal, so diffs from reorganization can be reviewed easily with limited scrutiny.

Answering `y` accepts the current state of the directory as the new baseline. Answer `n` if a modification looks wrong, and restore from a snapshot before running again.

The check is shallow by default: a file whose modification time has not changed since the last run keeps its stored checksum, which keeps routine checks fast on large trees. `--deep` ignores mtimes and rehashes everything. Corruption like bit rot not usually update mtime, so run a deep check now and then.

`arc.sum` sits next to `arc.conf` and is plain JSON.

### `arc restic[:profile] [args...]`

Runs `restic` from the `arc.conf` directory, merging in the flags, arguments, and environment variables from the config. Anything passed on the command line is forwarded through, so `arc restic snapshots`, `arc restic mount /mnt/x`, and `arc restic --version` behave as they would under plain `restic`.

Use `--dry-run` to print the resolved command without running it:

```sh
$ arc --dry-run restic:local backup
cwd:  /data/archive
env:  RESTIC_PASSWORD_FILE=/home/user/.restic-pass
exec: restic backup --exclude-file .resticignore --repo /mnt/backup/archive .
```

## Configuration

`arc.conf` is TOML, and every setting is optional.

```toml
[check]
hash-type = "sha256"        # sha256 (default), sha1, or md5
workers = 8                 # parallel hash workers (default: CPU count)
ignore = ["\\.DS_Store$"]   # regexes matched against paths relative to arc.conf

[restic]
environ = { RESTIC_PASSWORD_FILE = "~/.restic-pass" }

[restic.backup]
args = ["."]
exclude-file = ".resticignore"

[restic.profiles.local]
repo = "/mnt/backup/archive"

[restic.profiles.remote]
repo = "s3:s3.amazonaws.com/my-bucket"
environ = { AWS_PROFILE = "backup" }

[restic.profiles.remote.forget]
keep-daily = 7
keep-monthly = 12
prune = true
```

### The `[restic]` tree

Inside `[restic]`, keys become restic flags and nested tables become restic subcommands. Two keys are special: `args` supplies  arguments, and `environ` sets environment variables. Leading `~` is expanded in flag values, args, and environment values.

Flag values are rendered into args: `keep-daily = 7` becomes `--keep-daily 7`, `prune = true` becomes a bare `--prune`, `prune = false` is dropped, and an array repeats the flag (`tag = ["a", "b"]` gives `--tag a --tag b`). Single-character keys use a single dash.

### Profiles

Profiles let one directory target several repos. `arc restic:remote backup` uses the `remote` profile, and `arc restic backup` uses none.

Configuration is merged from most general to most specific:

1. `[restic]` (applies to every subcommand)
2. `[restic.<subcommand>]`
3. `[restic.profiles.<profile>]`
4. `[restic.profiles.<profile>.<subcommand>]`
5. command-line arguments

Flags and environment variables accumulate, with later layers winning on conflict. `args` is replaced outright by the most specific layer that sets it. Command-line arguments are always appended, and `args` from the config go last.

With the config above, `arc restic:remote forget --dry-run` resolves to:

```sh
restic forget --keep-daily 7 --keep-monthly 12 --prune \
  --repo s3:s3.amazonaws.com/my-bucket --dry-run
```
