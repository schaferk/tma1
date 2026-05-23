# TMA1 installer for Windows — downloads the latest tma1-server binary and registers a scheduled task.
#
# Install or upgrade:
#   irm https://tma1.ai/install.ps1 | iex
#
# Pin a specific version:
#   $env:TMA1_VERSION = 'v0.1.0'; irm https://tma1.ai/install.ps1 | iex
#
# Wire one or both agent adapters (hooks + MCP + /tma1-peer skill) in one shot:
#   $env:TMA1_ADAPTER = 'claude-code'; irm https://tma1.ai/install.ps1 | iex
#   $env:TMA1_ADAPTER = 'codex';        irm https://tma1.ai/install.ps1 | iex
#   $env:TMA1_ADAPTER = 'claude-code,codex'; irm https://tma1.ai/install.ps1 | iex
#   $env:TMA1_ADAPTER = 'all';          irm https://tma1.ai/install.ps1 | iex     # same as both
# (Adapter install via iex writes only global files — hooks, MCP entries,
# skills/commands. Project-local CLAUDE.md / AGENTS.md blocks are NOT touched
# because the script runs from whatever cwd; run `tma1-server install --adapter
# <name>` from a project dir to seed those.)
#
# Uninstall:
#   Unregister-ScheduledTask -TaskName 'TMA1 Server' -Confirm:$false
#   Remove-Item -Recurse -Force "$env:USERPROFILE\.tma1"

$ErrorActionPreference = 'Stop'

$Repo = 'tma1-ai/tma1'
$InstallDir = if ($env:TMA1_INSTALL_DIR) { $env:TMA1_INSTALL_DIR } else { Join-Path $env:USERPROFILE '.tma1\bin' }
$TMA1Port = if ($env:TMA1_PORT) { $env:TMA1_PORT } else { '14318' }
$TMA1DataDir = if ($env:TMA1_DATA_DIR) { $env:TMA1_DATA_DIR } else { Join-Path $env:USERPROFILE '.tma1' }
# Adapter(s) to wire into agents. Empty = skip. Accepts a comma-separated list
# or the alias `all` (= claude-code,codex). Each adapter registers hooks, MCP,
# and the /tma1-peer skill globally. Project-local files are skipped here —
# see Register-Adapter for why.
$TMA1Adapter = if ($env:TMA1_ADAPTER) { $env:TMA1_ADAPTER } else { '' }

function Write-Info  { param([string]$msg) Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Warn  { param([string]$msg) Write-Host "Warning: $msg" -ForegroundColor Yellow }

# --- Resolve latest release tag ---
function Resolve-Version {
    if ($env:TMA1_VERSION) { return $env:TMA1_VERSION }

    Write-Info 'Resolving latest version...'
    try {
        # GitHub redirects /releases/latest to the tag URL.
        $resp = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" `
            -MaximumRedirection 0 -ErrorAction SilentlyContinue -UseBasicParsing
    } catch {
        $resp = $_.Exception.Response
    }
    if ($resp -and $resp.Headers -and $resp.Headers.Location) {
        $loc = $resp.Headers.Location
        if ($loc -is [System.Collections.IEnumerable] -and $loc -isnot [string]) { $loc = $loc[0] }
        if ($loc -match '(v\d+\.\d+\.\d+.*)$') { return $Matches[1] }
    }

    # Fallback: most recent tag. The tags API returns results in reverse chronological
    # order, unlike the releases API which uses an unstable sort that breaks with
    # prerelease suffixes (e.g. alpha9 sorts above alpha10).
    $tags = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/tags?per_page=1"
    if ($tags -and $tags[0].name) { return $tags[0].name }

    throw 'Failed to resolve latest version. Set $env:TMA1_VERSION to install a specific version.'
}

# --- Stop existing service before upgrade ---
function Stop-ExistingService {
    $task = Get-ScheduledTask -TaskName 'TMA1 Server' -ErrorAction SilentlyContinue
    if (-not $task) { return }

    Write-Info 'Stopping existing TMA1 service...'

    # 1. Stop the scheduled task
    Stop-ScheduledTask -TaskName 'TMA1 Server' -ErrorAction SilentlyContinue

    # 2. Wait for the process launched from our install dir to exit.
    #    Match by executable path to avoid killing unrelated instances.
    $expectedPath = Join-Path $InstallDir 'tma1-server.exe'
    $retries = 0
    while ($retries -lt 30) {
        $proc = Get-Process -Name 'tma1-server' -ErrorAction SilentlyContinue |
            Where-Object { $_.Path -eq $expectedPath }
        if (-not $proc) { break }
        Start-Sleep -Seconds 1
        $retries++
    }

    # 3. Force-kill if still running after 30s
    $proc = Get-Process -Name 'tma1-server' -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -eq $expectedPath }
    if ($proc) { $proc | Stop-Process -Force }

    # 4. Unregister old task
    Unregister-ScheduledTask -TaskName 'TMA1 Server' -Confirm:$false -ErrorAction SilentlyContinue
}

# --- Download and verify ---
function Install-TMA1 {
    param([string]$Version)

    $archive = "tma1-server-windows-amd64.tar.gz"
    $url = "https://github.com/$Repo/releases/download/$Version/$archive"
    $checksumUrl = "$url.sha256sum"

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "tma1-install-$([guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
    $archivePath = Join-Path $tmpDir $archive

    try {
        Write-Info "Downloading $archive ($Version)..."
        Invoke-WebRequest -Uri $url -OutFile $archivePath -UseBasicParsing

        # Verify checksum
        Write-Info 'Verifying checksum...'
        try {
            $checksumFile = Join-Path $tmpDir 'checksum.txt'
            Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumFile -UseBasicParsing
            $checksumLine = Get-Content $checksumFile | Where-Object { $_ -like "*$archive*" } | Select-Object -First 1
            if ($checksumLine) {
                $expectedHash = ($checksumLine -split '\s+')[0]
                $actualHash = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
                if ($actualHash -ne $expectedHash) {
                    throw "Checksum mismatch: expected $expectedHash, got $actualHash"
                }
                Write-Info "Checksum verified."
            } else {
                Write-Warn 'Checksum entry not found, skipping verification.'
            }
        } catch [System.Net.WebException] {
            Write-Warn 'Checksum file not found, skipping verification.'
        }

        # Extract
        Write-Info "Extracting to $InstallDir..."
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        tar -xzf $archivePath -C $InstallDir

        # Create a `tma1.cmd` shim alongside tma1-server.exe. Symbolic links
        # on Windows require Developer Mode or admin, which most users don't
        # have; a thin .cmd wrapper avoids that constraint entirely. The Go
        # main.go dispatches on os.Args[1] regardless of argv[0], so both
        # invocations share behavior.
        $shimPath = Join-Path $InstallDir 'tma1.cmd'
        Set-Content -Path $shimPath -Value "@`"%~dp0tma1-server.exe`" %*" -Encoding ASCII
    } finally {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    }
}

# --- Register scheduled task (at logon, auto-restart) ---
function Register-TMA1Task {
    $binPath = Join-Path $InstallDir 'tma1-server.exe'
    if (-not (Test-Path $binPath)) {
        Write-Warn "Binary not found at $binPath. Skipping service registration."
        return
    }

    Write-Info 'Registering TMA1 as a scheduled task (runs at logon)...'

    # Pass runtime config as environment variables via cmd wrapper,
    # matching what the Unix installer does with launchd/systemd.
    $cmdArgs = "/c `"set `"TMA1_PORT=$TMA1Port`" && set `"TMA1_DATA_DIR=$TMA1DataDir`" && `"$binPath`"`""
    $action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument $cmdArgs
    # Scope to current user only, matching Unix installer's per-user service registration.
    # Use fully qualified DOMAIN\User identity so it works on domain-joined machines
    # and with Microsoft accounts (bare $env:USERNAME is ambiguous).
    $currentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
    $principal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) `
        -ExecutionTimeLimit ([TimeSpan]::Zero)

    Register-ScheduledTask -TaskName 'TMA1 Server' `
        -Action $action -Trigger $trigger -Settings $settings -Principal $principal `
        -Description 'TMA1 Server - LLM Agent Observability' `
        -Force | Out-Null

    # Start the task now
    Start-ScheduledTask -TaskName 'TMA1 Server'
    Write-Info 'TMA1 service started.'
}

# --- Adapter setup: register tma1 into one or more agents ---
# Parses $TMA1Adapter as a comma-separated list (or `all`, which expands to
# claude-code,codex) and invokes `tma1-server.exe install --adapter <name>
# --skip-project-files` for each. Idempotent on repeat install.
#
# Project-local files (CLAUDE.md / AGENTS.md instructions block, .gitignore
# entries) are intentionally skipped here: iex runs from whatever cwd the
# user happens to be in, so writing a block to a random directory's
# CLAUDE.md is worse than not writing one. Users wire project-local files
# later by `cd <project>; tma1-server install --adapter <name>`.
function Register-Adapter {
    if (-not $TMA1Adapter) { return }
    $binPath = Join-Path $InstallDir 'tma1-server.exe'
    if (-not (Test-Path $binPath)) {
        Write-Warn "Adapter requested ('$TMA1Adapter') but $binPath is missing; skipping."
        return
    }

    $list = if ($TMA1Adapter -eq 'all') { 'claude-code,codex' } else { $TMA1Adapter }
    $adapters = $list -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' }

    foreach ($name in $adapters) {
        if ($name -notin @('claude-code', 'codex')) {
            Write-Warn "Unknown adapter '$name' — skipping. Valid: claude-code, codex, all."
            continue
        }
        Write-Info "Registering $name adapter (hooks + MCP + skill, global-only)..."
        & $binPath install --adapter $name --skip-project-files
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "Adapter '$name' registration failed. Retry: `"$binPath`" install --adapter $name"
        }
    }
}

# --- Wait for health endpoint ---
function Wait-ForHealth {
    $url = "http://127.0.0.1:${TMA1Port}/health"
    Write-Info 'Waiting for TMA1 to become ready...'
    for ($i = 0; $i -lt 30; $i++) {
        try {
            $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2 -ErrorAction SilentlyContinue
            if ($resp.StatusCode -eq 200) {
                Write-Info 'TMA1 is running and healthy.'
                return
            }
        } catch {}
        Start-Sleep -Seconds 1
    }
    Write-Warn "TMA1 did not become ready within 30s. Check the process for errors."
}

# --- Post-install hints ---
# Branches on whether $TMA1Adapter was set:
#  - set     → adapter(s) wired; tell user how to add project-local files later
#  - empty   → wiring options (one-shot adapter vs manual OTel env vars)
function Show-PostInstall {
    $binPath = Join-Path $InstallDir 'tma1-server.exe'
    Write-Info "Installed tma1-server to $binPath  (alias: tma1.cmd)"
    Write-Host ''

    # PATH guidance is the first actionable item — everything else below
    # assumes `tma1` is callable. Conditional so repeat installs stay quiet
    # for users whose PATH is already set.
    if (-not ($env:PATH -split ';' | Where-Object { $_ -eq $InstallDir })) {
        Write-Info "Add $InstallDir to your PATH (one-time):"
        Write-Host "  [Environment]::SetEnvironmentVariable('PATH', `"$InstallDir;`" + [Environment]::GetEnvironmentVariable('PATH', 'User'), 'User')"
        Write-Host '  (run once in PowerShell, then open a new terminal)'
        Write-Host ''
    }

    Write-Host "Dashboard:  http://localhost:${TMA1Port}"
    Write-Host "Data dir:   $TMA1DataDir"
    Write-Host ''
    Write-Host 'Useful commands:'
    Write-Host '  tma1 install --adapter claude-code    # wire Claude Code (hooks + MCP + skill)'
    Write-Host '  tma1 install --adapter codex          # wire Codex'
    Write-Host '  tma1 build -- <command>               # wrap a build, tee output to TMA1'
    Write-Host '  tma1 uninstall --adapter <name>       # reverse an adapter install'
    Write-Host ''

    if ($TMA1Adapter) {
        Write-Host "Adapter(s) wired globally: $TMA1Adapter"
        Write-Host '  - Hooks, MCP server entry, and /tma1-peer skill installed for each.'
        Write-Host '  - Project-local CLAUDE.md / AGENTS.md blocks were NOT written here.'
        Write-Host '    To seed the TMA1 context block in a project, cd into it and run:'
        Write-Host '      tma1 install --adapter <claude-code|codex>'
        Write-Host ''
    } else {
        Write-Host 'Next: wire TMA1 into an agent.'
        Write-Host ''
        Write-Host 'Option A - One-shot adapter (recommended; hooks + MCP + /tma1-peer):'
        Write-Host '  tma1 install --adapter claude-code'
        Write-Host '  tma1 install --adapter codex'
        Write-Host '  (run from a project directory to also seed CLAUDE.md / AGENTS.md)'
        Write-Host ''
        Write-Host '  Or re-run this installer with $env:TMA1_ADAPTER set:'
        Write-Host "    `$env:TMA1_ADAPTER = 'claude-code'; irm https://tma1.ai/install.ps1 | iex"
        Write-Host "    `$env:TMA1_ADAPTER = 'claude-code,codex'; irm https://tma1.ai/install.ps1 | iex"
        Write-Host ''
        Write-Host 'Option B - Manual OTel config only (no hooks, no MCP, no skill):'
        Write-Host '  Claude Code (%USERPROFILE%\.claude\settings.json):'
        Write-Host '    "env": {'
        Write-Host "      `"OTEL_EXPORTER_OTLP_ENDPOINT`": `"http://localhost:${TMA1Port}/v1/otlp`","
        Write-Host '      "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",'
        Write-Host '      "OTEL_METRICS_EXPORTER": "otlp",'
        Write-Host '      "OTEL_LOGS_EXPORTER": "otlp"'
        Write-Host '    }'
        Write-Host ''
        Write-Host '  Codex (%USERPROFILE%\.codex\config.toml):'
        Write-Host '    [otel]'
        Write-Host '    log_user_prompt = true'
        Write-Host '    [otel.exporter.otlp-http]'
        Write-Host "    endpoint = `"http://localhost:${TMA1Port}/v1/logs`""
        Write-Host '    protocol = "binary"'
        Write-Host '    [otel.trace_exporter.otlp-http]'
        Write-Host "    endpoint = `"http://localhost:${TMA1Port}/v1/traces`""
        Write-Host '    protocol = "binary"'
        Write-Host '    [otel.metrics_exporter.otlp-http]'
        Write-Host "    endpoint = `"http://localhost:${TMA1Port}/v1/metrics`""
        Write-Host '    protocol = "binary"'
        Write-Host ''
    }

    Write-Host "GreptimeDB config:  $TMA1DataDir\config\standalone.toml"
    Write-Host '  (generated on first start; edit to tune CPU / memory limits)'
    Write-Host ''
}

# --- Main ---
function main {
    Write-Info 'Installing TMA1...'
    $version = Resolve-Version
    Stop-ExistingService
    Install-TMA1 -Version $version
    Register-TMA1Task
    Wait-ForHealth
    Register-Adapter
    Show-PostInstall
}

main
