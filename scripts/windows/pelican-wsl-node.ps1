#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Configures a Windows 11 system for running a Pelican Wings node via WSL2.

.DESCRIPTION
    This script:
    1. Verifies Windows 11
    2. Verifies WSL2 is installed
    3. Configures mirrored networking in .wslconfig
    4. Restarts WSL
    5. Adds firewall rules for game server ports (TCP+UDP 20000-25000)

.NOTES
    Run this script as Administrator in PowerShell.
#>

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "=== Pelican Wings WSL2 Node Setup ===" -ForegroundColor Cyan
Write-Host ""

# 1. Verify Windows 11
$osVersion = [System.Environment]::OSVersion
$build = [int](Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion").CurrentBuildNumber

if ($build -lt 22000) {
    Write-Host "ERROR: Windows 11 (build 22000+) is required. Current build: $build" -ForegroundColor Red
    exit 1
}
Write-Host "[OK] Windows 11 detected (build $build)" -ForegroundColor Green

# 2. Verify WSL2
try {
    $wslStatus = wsl --status 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "ERROR: WSL2 is not installed or not functioning." -ForegroundColor Red
        Write-Host "Install WSL2 with: wsl --install" -ForegroundColor Yellow
        exit 1
    }
    Write-Host "[OK] WSL2 is installed" -ForegroundColor Green
} catch {
    Write-Host "ERROR: Could not run 'wsl --status'. Is WSL2 installed?" -ForegroundColor Red
    Write-Host "Install WSL2 with: wsl --install" -ForegroundColor Yellow
    exit 1
}

# 3. Configure .wslconfig with mirrored networking
$wslConfigPath = Join-Path $env:USERPROFILE ".wslconfig"

$desiredContent = @"
[wsl2]
networkingMode=mirrored
"@

if (Test-Path $wslConfigPath) {
    $existing = Get-Content $wslConfigPath -Raw
    if ($existing -match "(?i)networkingMode\s*=\s*mirrored") {
        Write-Host "[OK] Mirrored networking already configured in .wslconfig" -ForegroundColor Green
    } else {
        # Back up existing config
        $backup = "$wslConfigPath.bak.$(Get-Date -Format 'yyyyMMddHHmmss')"
        Copy-Item $wslConfigPath $backup
        Write-Host "Backed up existing .wslconfig to $backup" -ForegroundColor Yellow

        # Check if [wsl2] section exists
        if ($existing -match "(?i)\[wsl2\]") {
            # Add networkingMode under existing [wsl2] section
            $updated = $existing -replace "(?i)(\[wsl2\])", "`$1`nnetworkingMode=mirrored"
            Set-Content $wslConfigPath $updated -NoNewline
        } else {
            # Append [wsl2] section
            Add-Content $wslConfigPath "`n$desiredContent"
        }
        Write-Host "[OK] Added networkingMode=mirrored to .wslconfig" -ForegroundColor Green
    }
} else {
    Set-Content $wslConfigPath $desiredContent
    Write-Host "[OK] Created .wslconfig with mirrored networking" -ForegroundColor Green
}

# 4. Restart WSL
Write-Host ""
Write-Host "Shutting down WSL to apply networking changes..." -ForegroundColor Yellow
wsl --shutdown
Start-Sleep -Seconds 2
Write-Host "[OK] WSL has been shut down" -ForegroundColor Green

# 5. Add firewall rules
Write-Host ""
Write-Host "Configuring Windows Firewall rules..." -ForegroundColor Yellow

$rules = @(
    @{ Name = "Pelican WSL TCP"; Protocol = "TCP" },
    @{ Name = "Pelican WSL UDP"; Protocol = "UDP" }
)

foreach ($rule in $rules) {
    $existing = Get-NetFirewallRule -DisplayName $rule.Name -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Host "[OK] Firewall rule '$($rule.Name)' already exists" -ForegroundColor Green
    } else {
        New-NetFirewallRule `
            -DisplayName $rule.Name `
            -Direction Inbound `
            -Protocol $rule.Protocol `
            -LocalPort "20000-25000" `
            -Action Allow `
            -Profile Any | Out-Null
        Write-Host "[OK] Created firewall rule '$($rule.Name)' (inbound $($rule.Protocol) 20000-25000)" -ForegroundColor Green
    }
}

# 6. Print verification instructions
Write-Host ""
Write-Host "=== Setup Complete ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "Next steps:" -ForegroundColor Yellow
Write-Host "  1. Start your WSL distribution (e.g., 'wsl' in a new terminal)"
Write-Host "  2. Verify mirrored networking: ip addr show eth0"
Write-Host "     (The IP should match your Windows host IP)"
Write-Host "  3. Install Docker Engine inside WSL if not already installed"
Write-Host "  4. Ensure your Wings data directory is inside the WSL filesystem"
Write-Host "     (e.g., /var/lib/pelican, NOT /mnt/c/...)"
Write-Host "  5. Start Wings as usual"
Write-Host ""
