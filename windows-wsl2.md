# Running Wings on Windows (WSL2)

Wings is a Linux daemon. On Windows it runs **inside WSL2** as an ordinary Linux
binary, and Docker Engine runs inside that same WSL2 distribution. Wings is not a
native Windows program and does not use Windows containers — every game server is
a Linux container, exactly as on a Linux node.

> **Design note:** the full support policy and rationale live in
> [`windows.md`](./windows.md). This page is the setup runbook.

## Support policy (read this first)

A Windows node is supported **only** when all of the following are true:

- Windows 11
- WSL2 installed and updated
- WSL networking set to **`mirrored`** (required — see below)
- Wings runs inside WSL2, with Docker Engine inside the same distro
- The server data root lives on the **WSL ext4 filesystem** (not `/mnt/c`)
- Windows Firewall allows your configured port ranges

**Why mirrored networking is required.** Game servers depend heavily on inbound
UDP. WSL NAT mode has no reliable built-in UDP forwarding (`netsh … portproxy` is
TCP-only). Mirrored mode removes the NAT layer and exposes Windows interfaces
directly to WSL, giving reliable inbound TCP **and** UDP. Without mirrored
networking, Wings refuses to start (unless you explicitly opt into the
unsupported NAT mode, which blocks UDP).

## Setup

### 1. Install and update WSL2

In an elevated PowerShell:

```powershell
wsl --install
wsl --update
```

Install a distribution (e.g. Ubuntu) if you don't have one, and make sure it is
running as WSL **2** (`wsl -l -v`).

### 2. Enable mirrored networking

Create or edit `%USERPROFILE%\.wslconfig` and add:

```ini
[wsl2]
networkingMode=mirrored
```

Then restart WSL so it takes effect:

```powershell
wsl --shutdown
```

> The helper script in `scripts/windows/pelican-wsl-node.ps1` automates this step
> and the firewall rules below. Run it from an elevated PowerShell.

### 3. Install Docker Engine inside WSL2

Inside your WSL2 distro, install Docker Engine (the Linux Docker, **not** Docker
Desktop's Windows integration) and confirm it runs:

```bash
docker info
```

### 4. Put the data root on ext4

Keep the server data root on the WSL ext4 filesystem — for example
`/var/lib/pelican` — **never** under `/mnt/c` or any other Windows-mounted path.
Windows mounts (drvfs/9p) cause slow I/O, permission inconsistencies, inotify
problems, and file-locking anomalies, so Wings refuses to start if the data root
is on one.

### 5. Configure Wings

The WSL-specific options live under `system:` in `config.yml`. The defaults below
are what Wings ships with:

```yaml
system:
  data: /var/lib/pelican/volumes

  # Resource-limit enforcement. Automatically downgraded to best_effort in WSL.
  # enforced | best_effort | off
  limits_mode: best_effort

  wsl:
    networking_mode: auto        # auto | mirrored | nat  (auto reads .wslconfig)
    require_mirrored: true
    allow_unsupported_nat: false

  storage:
    enforce_wsl_ext4: true
    allow_windows_mount_override: false

  ports:
    tcp_range_start: 20000
    tcp_range_end: 25000
    udp_range_start: 20000
    udp_range_end: 25000
    max_ports_per_server: 32
```

Wings enforces these on WSL nodes: every server allocation must fall inside the
configured TCP **and** UDP ranges, and no server may exceed
`max_ports_per_server`. Choose ranges that match the firewall rules in the next
step.

### 6. Configure Windows Firewall

Allow inbound traffic on your port ranges (elevated PowerShell — adjust the range
to match your config):

```powershell
New-NetFirewallRule -DisplayName "Pelican WSL UDP" -Direction Inbound `
  -Protocol UDP -LocalPort 20000-25000 -Action Allow
New-NetFirewallRule -DisplayName "Pelican WSL TCP" -Direction Inbound `
  -Protocol TCP -LocalPort 20000-25000 -Action Allow
```

### 7. Start Wings

Run Wings inside the WSL2 distro as you would on any Linux node. On boot it
detects WSL, validates mirrored networking and the data root, and logs
`WSL mirrored networking validated successfully`. The node reports
`platform: windows_wsl` to the panel along with its networking, storage, and
limits posture.

## Unsupported: NAT mode

If you cannot enable mirrored networking you may still start the node by setting:

```yaml
system:
  wsl:
    allow_unsupported_nat: true
```

In this mode the node is reported as **unsupported**, and because inbound UDP is
unavailable, **any server with a port allocation will fail to start** with a clear
error. Use a Linux node or enable mirrored networking for production game hosting.

## Known limitations

- Mirrored networking can interact poorly with some VPN setups.
- Windows updates may change WSL networking behavior — re-verify after major updates.
- Multicast/broadcast LAN discovery is not guaranteed.
- Sleep/hibernate can interrupt running services.
- Antivirus scanning may degrade performance. Configure AV exclusions yourself if
  needed — Wings does not modify AV settings.
