# Running Wings on macOS

Wings is designed for Linux, but it can compile and run natively on macOS. This
document covers how to set up Wings on macOS and the code changes that make it
possible.

> **Note:** Wings manages Docker containers that run Linux. On macOS, Docker
> Desktop provides a Linux VM transparently. Game servers and other containers
> run inside that VM — Wings itself runs on the macOS host.

## Prerequisites

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) installed
  and running
- [mkcert](https://github.com/FiloSottile/mkcert) for SSL certificates
  (`brew install mkcert`)
- A Pelican Panel instance with a node configured for this machine

## Setup

### 1. Create directories

```bash
mkdir -p ~/.config/pelican
mkdir -p ~/.local/share/pelican/{logs,volumes,archives,backups}
mkdir -p ~/.pelican/tmp
```

### 2. Generate SSL certificates

Wings requires HTTPS. Use mkcert to generate locally-trusted certificates:

```bash
mkcert -install
mkcert -cert-file ~/.config/pelican/localhost.pem \
       -key-file ~/.config/pelican/localhost-key.pem \
       localhost 127.0.0.1
```

### 3. Docker socket symlink

Docker Desktop places its socket at `~/.docker/run/docker.sock`, but Wings
expects `/var/run/docker.sock`:

```bash
sudo ln -sf ~/.docker/run/docker.sock /var/run/docker.sock
```

### 4. Configure Wings

After creating the node in the Panel, copy the auto-generated config from
`/etc/pelican/config.yml` (or use `wings configure`) and save it to
`~/.config/pelican/config.yml`. Modify the following settings:

```yaml
api:
  ssl:
    enabled: true
    cert: /Users/<you>/.config/pelican/localhost.pem
    key: /Users/<you>/.config/pelican/localhost-key.pem
system:
  root_directory: /Users/<you>/.local/share/pelican
  log_directory: /Users/<you>/.local/share/pelican/logs
  data: /Users/<you>/.local/share/pelican/volumes
  archive_directory: /Users/<you>/.local/share/pelican/archives
  backup_directory: /Users/<you>/.local/share/pelican/backups
  tmp_directory: /Users/<you>/.pelican/tmp
  user:
    uid: 501    # your UID (run `id -u`)
    gid: 20     # your GID (run `id -g`)
    passwd:
      enable: true
      directory: /Users/<you>/.config/pelican
  machine_id:
    enable: false
  check_permissions_on_boot: false
  enable_log_rotate: false
```

Replace `<you>` with your macOS username.

**Why these settings matter:**

- **All paths under `/Users/`** — Docker Desktop's file sharing only grants the
  VM access to paths under `/Users`, `/Volumes`, `/private`, and `/tmp` by
  default. Paths like `/var/lib/pelican` will not be accessible from inside
  containers.
- **`uid`/`gid` set to your user** — Wings won't try to create a system user
  via `useradd`.
- **`machine_id.enable: false`** — Avoids a bind mount of `/etc/machine-id`
  which doesn't exist on macOS.
- **`check_permissions_on_boot: false`** — Prevents Wings from trying to `chown`
  server data directories to a pelican system user.
- **`enable_log_rotate: false`** — macOS doesn't have `/etc/logrotate.d/`.

### 5. Panel node configuration

In the Panel, configure the node with:

- **FQDN:** `localhost` or `127.0.0.1`
- **Port:** `8080`
- **SSL:** enabled
- **Scheme:** HTTPS

### 6. Start Wings

```bash
./wings --config ~/.config/pelican/config.yml
```

No `sudo` required. Do not run Wings with `sudo` on macOS — Docker Desktop's VM
accesses host files as the host user. If Wings runs as root, it creates
directories owned by `root` that the VM cannot write to, causing containers to
fail on bind-mounted volumes.

If the Panel shows "is not Pelican Wings!" after startup, clear the Panel cache:

```bash
php artisan cache:clear
```

## Building from Source

```bash
# Native build (current architecture)
go build -o wings wings.go

# Or use the Makefile targets
make build-darwin
```

---

## Code Changes

The sections below document the platform-specific code changes for developers.

### Platform-Specific File Splits

The core compilation work splits Linux-only syscalls into `_linux.go` and
`_darwin.go` file pairs using Go's filename-based build tag convention.

#### `internal/ufs/` — Unix Filesystem Layer

| File | Purpose |
|------|---------|
| `file_linux.go` | `O_LARGEFILE = unix.O_LARGEFILE` |
| `file_darwin.go` | `O_LARGEFILE = 0` (no-op on macOS) |
| `fs_linux.go` | `_openat2()` using `unix.Openat2`, `fdPath()` via `/proc/self/fd/`, `Chtimesat()` using `unix.UTIME_OMIT` |
| `fs_darwin.go` | `_openat2()` stub returning `ENOSYS`, `fdPath()` via `F_GETPATH` fcntl, `Chtimesat()` that reads current timestamps when zero |
| `walk_linux.go` | `getdents()` using `unix.Getdents`, `nameFromDirent()` with NUL scan |
| `walk_darwin.go` | `getdents()` using `unix.Getdirentries`, `nameFromDirent()` using `Dirent.Namlen` |

**Why these splits are needed:**

- `unix.O_LARGEFILE` does not exist on Darwin (all files support large offsets).
- `unix.Openat2` / `unix.OpenHow` / `unix.RESOLVE_BENEATH` are Linux 5.6+ only.
- `/proc/self/fd/` does not exist on macOS; `F_GETPATH` fcntl is the equivalent.
- `unix.UTIME_OMIT` does not exist on Darwin.
- `unix.Getdents` does not exist on Darwin; `unix.Getdirentries` is used instead.
- Darwin `Dirent` has a `Namlen` field; Linux requires scanning for NUL.

#### `config/` — OpenAt2 Detection

| File | Purpose |
|------|---------|
| `config_openat_linux.go` | Full `UseOpenat2()` with runtime probe via `unix.Openat2` |
| `config_openat_darwin.go` | `UseOpenat2()` always returns `false` |

#### `server/filesystem/` — File Stat CTime

| File | Purpose |
|------|---------|
| `stat_linux.go` | Original `CTime()` using `unix.Stat_t.Ctim` (was `//go:build linux`) |
| `stat_darwin.go` | `CTime()` handling both `unix.Stat_t.Ctim` and `syscall.Stat_t.Ctimespec` |

`golang.org/x/sys/unix` v0.35.0 normalizes `Stat_t` field names (`Mtim`,
`Ctim`) across Linux and Darwin, but `syscall.Stat_t` still uses
`Ctimespec`/`Mtimespec` on Darwin.

### Runtime Guards

These changes use `runtime.GOOS` checks to handle macOS differences at runtime.

#### `config/config.go`

- **`getSystemName()`** — Returns `"darwin"` immediately on macOS instead of
  calling `osrelease.Read()` (which reads `/usr/lib/os-release`, a file that
  does not exist on macOS).
- **`EnsurePelicanUser()`** — On macOS, uses `user.Current()` to get the
  running user instead of calling `useradd` (which does not exist on macOS).

#### `system/system.go`

- **`GetSystemInformation()`** — Returns `"macOS"` as the OS name on Darwin
  instead of reading `/usr/lib/os-release`.
- **`getSystemName()`** — Same early return as `config/config.go`.

#### `environment/settings.go`

- **`AsContainerResources()`** — Only sets `BlkioWeight` (IO weight) on Linux.
  Docker Desktop's Linux VM does not support `io.weight` in its cgroup
  configuration, so setting it on macOS causes container creation to fail.
