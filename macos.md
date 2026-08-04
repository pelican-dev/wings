# Running Wings on macOS

Wings is designed for Linux, but it can compile and run natively on macOS. This
document covers how to set up Wings on macOS.

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

## Developer Notes

Platform-specific behavior lives in `_linux.go` / `_darwin.go` file pairs
(Go's filename-based build constraints) in `internal/ufs/`, `config/`, and
`server/filesystem/`, with a few `runtime.GOOS` checks where a full file split
would be overkill. The rationale for each difference is documented alongside
the code.
