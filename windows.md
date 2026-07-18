# Pelican Wings – Windows Node Support via WSL2 (Mirrored Networking Required)

## Overview

This document defines the official implementation plan for supporting Windows nodes in Pelican Wings using Windows Subsystem for Linux 2 (WSL2).

Windows-native containers are not supported.

Instead, Wings runs inside WSL2 and uses Docker Engine inside that Linux environment.

To ensure reliable inbound UDP and TCP connectivity for game servers, WSL2 **mirrored networking mode is required**.

NAT mode is not supported by default.

This document contains:

- Support policy
- Enforcement rules
- Configuration schema additions
- Startup validation logic
- Networking rules
- Storage rules
- Windows helper script requirements
- Documentation requirements
- Known limitations
- Implementation checklist for Codex/Claude

This document is intended to be copied directly into the repository and used as an implementation specification.

---

# 1. Support Policy

## 1.1 Supported Configuration

Windows nodes are supported only when:

- Windows 11
- WSL2 installed and updated
- WSL networking mode set to `mirrored`
- Wings runs inside WSL2
- Docker Engine runs inside WSL2
- Server data root is inside the WSL ext4 filesystem
- Windows Firewall rules allow required port ranges

If any of these conditions are not met, the node is not supported.

---

## 1.2 Not Supported

The following are explicitly not supported:

- Windows-native containers
- Running Wings directly on Windows without WSL2
- WSL NAT networking mode (unless override is explicitly enabled)
- Server data stored under `/mnt/c` or any Windows-mounted filesystem
- Multicast/broadcast LAN discovery guarantees
- Sleep/hibernate production reliability

---

# 2. Why Mirrored Networking Is Required

Game servers rely heavily on inbound UDP.

WSL NAT mode does not provide a reliable built-in UDP forwarding mechanism.

Windows `netsh interface portproxy` supports TCP only.

Mirrored mode removes the NAT layer and mirrors Windows network interfaces directly into WSL.

This allows:

- Reliable inbound UDP
- Reliable inbound TCP
- LAN reachability without manual port proxy hacks
- Behavior closer to a native Linux node

Because UDP is extremely common in game hosting, mirrored mode is required.

---

# 3. Enforcement Rules in Wings

## 3.1 WSL Detection

On startup, Wings must detect whether it is running inside WSL.

Acceptable detection methods include:

- Presence of `WSL_INTEROP` environment variable
- `/proc/version` containing "Microsoft"
- Kernel release containing "microsoft-standard"

If WSL is not detected, no Windows enforcement applies.

If WSL is detected, Windows rules apply.

---

## 3.2 Mirrored Networking Enforcement

Add configuration fields:

    wsl:
        networking_mode: auto | mirrored | nat
        require_mirrored: true
        allow_unsupported_nat: false

Default behavior:

- If WSL detected
- And networking mode resolves to anything other than mirrored
- And require_mirrored is true
- Then Wings must refuse to start

Error message must clearly state:

    ERROR: WSL detected without mirrored networking enabled.

    To enable mirrored networking, create or edit:
        %USERPROFILE%\.wslconfig

    Add:

        [wsl2]
        networkingMode=mirrored

    Then run:

        wsl --shutdown

    Restart WSL and relaunch Wings.

If allow_unsupported_nat is set to true:

- Wings may start
- Node must be marked "Unsupported (NAT mode)"
- UDP allocations must be blocked

---

## 3.3 UDP Allocation Rules

When node is in NAT mode (unsupported override):

- If any server attempts to allocate a UDP port
- Server startup must fail with:

    ERROR: UDP inbound is not supported in WSL NAT mode.
    Enable mirrored networking or use a Linux node.

When mirrored mode is active:

- UDP and TCP allocations are allowed normally.

No egg metadata is required.

Detection is based on actual allocated ports.

---

# 4. Storage Enforcement

## 4.1 Data Root Requirements

When running inside WSL:

- data_root must NOT reside on:
    - `/mnt/*`
    - any mount of type `drvfs`
    - any Windows-mounted filesystem
    - any 9p mount backed by Windows

Add configuration:

    storage:
        enforce_wsl_ext4: true
        allow_windows_mount_override: false

Default:

- enforce_wsl_ext4 = true
- allow_windows_mount_override = false

If data_root is detected on a Windows mount and enforcement is true:

Wings must refuse to start with:

    ERROR: Server data directory is on a Windows-mounted filesystem.
    Move the data root to a directory inside the WSL ext4 filesystem
    (example: /var/lib/pelican)

This is mandatory because Windows mounts cause:

- Slow I/O
- Permission inconsistencies
- inotify issues
- File locking anomalies

---

# 5. Port Allocation Requirements

Add configuration:

    ports:
        tcp_range_start: 20000
        tcp_range_end: 25000
        udp_range_start: 20000
        udp_range_end: 25000
        max_ports_per_server: 32

Rules:

- Port ranges must be explicitly configured.
- Wings must refuse to allocate ports outside these ranges.
- Wings must refuse to start if ranges are invalid or overlapping.

This ensures predictable firewall configuration.

---

# 6. Resource Limits

Add configuration:

    limits_mode: enforced | best_effort | off

Defaults:

- Linux nodes: enforced
- WSL nodes: best_effort

best_effort means:

- Apply Docker limits if supported
- Do not guarantee strict enforcement
- Log warnings if limits cannot be applied

---

# 7. Node Feature Reporting

Windows WSL nodes must report:

    platform: windows_wsl
    networking: mirrored
    udp_inbound: true
    limits_mode: best_effort
    storage: ext4_only

If NAT override enabled:

    networking: nat
    udp_inbound: false
    support_level: unsupported

Panel should display this clearly.

---

# 8. Windows Helper Script

Create:

    scripts/windows/pelican-wsl-node.ps1

The script must:

1. Verify Windows 11
2. Verify WSL2 installed
3. Write %USERPROFILE%\.wslconfig:

        [wsl2]
        networkingMode=mirrored

4. Execute:

        wsl --shutdown

5. Add Windows Firewall rules for configured port ranges:

        New-NetFirewallRule -DisplayName "Pelican WSL UDP" -Direction Inbound `
          -Protocol UDP -LocalPort 20000-25000 -Action Allow

        New-NetFirewallRule -DisplayName "Pelican WSL TCP" -Direction Inbound `
          -Protocol TCP -LocalPort 20000-25000 -Action Allow

6. Print verification instructions

Script is optional but recommended.

---

# 9. Documentation Requirements

Create a dedicated documentation page:

    Windows Node (WSL2) Setup

Must include:

- Requirements checklist
- Enabling mirrored networking
- Restarting WSL
- Setting data root inside ext4
- Configuring port ranges
- Configuring Windows Firewall
- Known limitations

---

# 10. Known Limitations

Document the following:

- Mirrored networking may interact poorly with some VPN setups.
- Windows updates may affect WSL networking behavior.
- Multicast/broadcast discovery is not guaranteed.
- Sleep/hibernate can interrupt services.
- Antivirus scanning may cause performance degradation.

Do not attempt to automatically configure AV exclusions.

Only mention in docs.

---

# 11. Implementation Checklist (For Codex / Claude)

1. Add WSL detection helper
2. Add networking mode detection logic
3. Add config fields under `wsl`, `storage`, `ports`
4. Implement startup validation order:
    - Detect WSL
    - Validate mirrored mode
    - Validate data root
    - Validate port ranges
5. Implement UDP allocation blocking when NAT
6. Implement node feature reporting
7. Implement limits_mode behavior
8. Add helper PowerShell script
9. Add documentation page
10. Add integration test plan for Windows 11 VM

---

# 12. Final Policy Decision

Windows node support is:

    Wings inside WSL2
    Mirrored networking required
    Data root inside ext4 required
    Explicit port ranges required

This keeps Windows hosting predictable and avoids UDP support chaos.

If mirrored networking cannot be enabled, the node should be considered unsupported for production game hosting.

