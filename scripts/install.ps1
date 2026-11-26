# Install the latest Orchard release on Windows, and optionally have it start
# on its own.
#
#   irm https://raw.githubusercontent.com/Jishnu-Prasad888/Orchard/main/scripts/install.ps1 | iex
#
# It picks the build for this CPU, checks it against the release's SHA256SUMS,
# installs it to %LOCALAPPDATA%\Programs\Orchard, adds that to your PATH, and
# then asks (y/n) whether to register a scheduled task so Orchard starts
# automatically. Run from an elevated (Administrator) PowerShell, the task
# starts Orchard at boot, even before anyone signs in; otherwise it starts
# when you sign in. Running it again upgrades in place.
#
# Windows has no systemd; a scheduled task is the closest equivalent, since
# Orchard isn't built as a Windows service.
#
# Options, when saved to a file and run (.\install.ps1 -Yes):
#   -Yes            take the default answer to every question
#   -Version v1.4.0 install that release instead of the latest
#   -InstallDir DIR install into DIR
#   -NoService      install the binary only; don't ask about autostart
#
# With `irm | iex`, set the same through the environment first, e.g.
#   $env:ORCHARD_VERSION = 'v1.4.0'; irm .../install.ps1 | iex
# (ORCHARD_VERSION, ORCHARD_INSTALL_DIR, ORCHARD_YES=1, ORCHARD_NO_SERVICE=1,
# ORCHARD_REPO).

# Everything is inside a function, called on the last line: a truncated
# download runs nothing, and nothing here calls `exit`, which under `iex`
# would close the window you ran it from.
function Install-Orchard {
    [CmdletBinding()]
    param(
        [switch]$Yes,
        [string]$Version = $env:ORCHARD_VERSION,
        [string]$InstallDir = $env:ORCHARD_INSTALL_DIR,
        [switch]$NoService
    )

    $ErrorActionPreference = 'Stop'
    # Invoke-WebRequest's progress bar makes downloads many times slower on
    # Windows PowerShell 5.1.
    $ProgressPreference = 'SilentlyContinue'
    if ($env:ORCHARD_YES -eq '1') { $Yes = $true }
    if ($env:ORCHARD_NO_SERVICE -eq '1') { $NoService = $true }

    $Repo = if ($env:ORCHARD_REPO) { $env:ORCHARD_REPO } else { 'Jishnu-Prasad888/Orchard' }
    $Port = 8090
    $TaskName = 'Orchard'

    function Step([string]$msg) { Write-Host '==> ' -ForegroundColor Green -NoNewline; Write-Host $msg }
    function Warn([string]$msg) { Write-Host "warning: $msg" -ForegroundColor Yellow }

    # Ask "Question?" $true|$false - returns the answer as a bool.
    function Ask([string]$question, [bool]$default) {
        $hint = if ($default) { '[Y/n]' } else { '[y/N]' }
        if ($Yes -or -not [Environment]::UserInteractive) {
            Write-Host "$question $hint ($(if ($default) { 'y' } else { 'n' }))"
            return $default
        }
        while ($true) {
            $answer = (Read-Host "$question $hint").Trim()
            if ($answer -eq '') { return $default }
            if ($answer -match '^(y|yes)$') { return $true }
            if ($answer -match '^(n|no)$') { return $false }
            Write-Host 'Please answer y or n.'
        }
    }

    # The version a binary reports, or ''. Releases before v1.4.0 don't know
    # --version and start the server instead, so give up after a few seconds
    # and stop it.
    function Get-BinVersion([string]$path) {
        $out = [IO.Path]::GetTempFileName()
        $err = [IO.Path]::GetTempFileName()
        try {
            $p = Start-Process -FilePath $path -ArgumentList '--version' -NoNewWindow -PassThru `
                -RedirectStandardOutput $out -RedirectStandardError $err
            if (-not $p.WaitForExit(4000)) { $p.Kill(); return '' }
            $line = Get-Content $out -TotalCount 1
            if ($line -match '^v\d') { return $line.Trim() }
            return ''
        } catch { return '' } finally { Remove-Item $out, $err -ErrorAction SilentlyContinue }
    }

    # ---------------------------------------------------------- platform ---

    $onWindows = ($PSVersionTable.PSEdition -eq 'Desktop') -or $IsWindows
    if (-not $onWindows) {
        Write-Host 'This installer is for Windows. On Linux or macOS, run:' -ForegroundColor Yellow
        Write-Host "  curl -fsSL https://raw.githubusercontent.com/$Repo/main/scripts/install.sh | bash"
        return
    }

    # PROCESSOR_ARCHITEW6432 is set when a 32-bit PowerShell runs on 64-bit Windows.
    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($cpu) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' {
            # No native arm64 Windows build yet; Windows 11 on Arm runs x64
            # programs through emulation.
            $arch = 'amd64'
            Warn 'there is no native Arm build for Windows yet; installing the x64 build, which Windows 11 on Arm runs under emulation'
        }
        default { throw "No Orchard build for a $cpu CPU; Windows builds exist for x64 (amd64)." }
    }
    $asset = "orchard-windows-$arch.exe"
    Step "Detected windows/$cpu"

    # TLS 1.2 isn't on by default in Windows PowerShell 5.1 on older systems.
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

    if ($Version) {
        if (-not $Version.StartsWith('v')) { $Version = "v$Version" }
    } else {
        $release = Invoke-RestMethod -UseBasicParsing -TimeoutSec 60 -Headers @{ 'User-Agent' = 'orchard-installer' } `
            -Uri "https://api.github.com/repos/$Repo/releases/latest"
        $Version = $release.tag_name
        if (-not $Version) { throw "Could not find the latest release of $Repo (is it published?)" }
        Step "Latest release is $Version"
    }

    # ------------------------------------------------------------ binary ---

    if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\Orchard' }
    $bin = Join-Path $InstallDir 'orchard.exe'
    $current = ''
    if (Test-Path $bin) {
        $current = Get-BinVersion $bin
        $shown = if ($current) { $current } else { '(unknown version)' }
        Step "Found Orchard $shown at $bin"
    }

    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
    $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    $upgraded = $false

    if ($current -eq $Version) {
        Step "Orchard $Version is already installed"
    } else {
        $tmp = Join-Path ([IO.Path]::GetTempPath()) ("orchard-install-" + [guid]::NewGuid())
        New-Item -ItemType Directory -Path $tmp | Out-Null
        try {
            $base = "https://github.com/$Repo/releases/download/$Version"
            Step "Downloading $asset $Version"
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 60 -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')

            # Lines look like "<hash>  dist/orchard-windows-amd64.exe".
            $want = $null
            foreach ($line in Get-Content (Join-Path $tmp 'SHA256SUMS')) {
                $parts = $line -split '\s+', 2
                if ($parts.Count -eq 2 -and (Split-Path -Leaf $parts[1].TrimStart('*')) -eq $asset) { $want = $parts[0] }
            }
            if (-not $want) { throw "SHA256SUMS has no entry for $asset" }
            $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash
            if ($got -ne $want.ToUpperInvariant()) { throw "Checksum mismatch for $asset; not installing it" }
            Step 'Checksum verified'

            # '' means a release older than v1.4.0, which can't report its version.
            $reported = Get-BinVersion (Join-Path $tmp $asset)
            if ($reported -and $reported -ne $Version) { throw "The downloaded binary reports version '$reported', expected $Version" }

            # A running orchard.exe can't be overwritten: stop it first.
            if ($task -and $task.State -eq 'Running') { Stop-ScheduledTask -TaskName $TaskName }
            Get-Process -Name orchard -ErrorAction SilentlyContinue |
                Where-Object { $_.Path -eq $bin } | Stop-Process -Force
            Start-Sleep -Milliseconds 500

            New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
            if (Test-Path $bin) { Copy-Item -Force $bin "$bin.previous" }
            Move-Item -Force (Join-Path $tmp $asset) $bin
            $upgraded = $true
            $note = if ($current) { " (the previous binary is at $bin.previous)" } else { '' }
            Step "Installed Orchard $Version to $bin$note"
        } finally {
            Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        }
    }

    # Put the install directory on the user's PATH (and this session's).
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $InstallDir)) {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        $env:Path = "$env:Path;$InstallDir"
        Step "Added $InstallDir to your PATH (open a new terminal to use 'orchard')"
    }

    # --------------------------------------------------------- autostart ---

    if ($NoService) {
        Write-Host ''
        Write-Host "Done. Start Orchard with: $bin"
        Write-Host "Then open http://127.0.0.1:$Port and create the administrator account."
        return
    }

    Write-Host ''
    $when = if ($isAdmin) { 'at boot' } else { 'when you sign in' }
    if (-not (Ask "Set up a scheduled task so Orchard starts automatically ($when)?" $true)) {
        if ($upgraded -and $task -and (Ask "Restart the existing Orchard task to run $Version?" $true)) {
            Start-ScheduledTask -TaskName $TaskName
        }
        Write-Host "Start Orchard with: $bin"
        Write-Host "Then open http://127.0.0.1:$Port and create the administrator account."
        return
    }
    if (-not $isAdmin) {
        Write-Host '(To start at boot, even before anyone signs in, re-run this from an Administrator PowerShell.)'
    }

    $lan = Ask 'Make Orchard reachable from other devices on your network? Answer n to keep it on this machine only.' $false
    $listen = if ($lan) { "0.0.0.0:$Port" } else { "127.0.0.1:$Port" }

    # The task runs this launcher, which holds Orchard's settings (the
    # counterpart of the env file on Linux). Edit it and restart the task to
    # change them; every ORCHARD_* setting is listed in the README.
    $launcher = Join-Path $InstallDir 'orchard-start.cmd'
    @(
        '@echo off'
        'rem Started by the "Orchard" scheduled task. Add ORCHARD_* settings below,'
        'rem e.g. set "ORCHARD_DATA_DIR=D:\Orchard", then restart the task.'
        "set `"ORCHARD_HTTP_ADDR=$listen`""
        '"%~dp0orchard.exe"'
    ) | Set-Content -Encoding Ascii -Path $launcher
    Step "Wrote $launcher"

    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
        -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)

    if ($isAdmin) {
        # S4U runs the task as you, at boot and in the background, without
        # storing your password. It can't reach network shares, though: for a
        # library on a NAS share, run this without Administrator instead.
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType S4U -RunLevel Limited
        $action = New-ScheduledTaskAction -Execute $launcher -WorkingDirectory $InstallDir
    } else {
        # A task in your signed-in session would open a console window;
        # conhost's headless mode keeps it out of sight.
        $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
        $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
        $action = New-ScheduledTaskAction -Execute 'conhost.exe' -Argument "--headless `"$launcher`"" -WorkingDirectory $InstallDir
    }

    Step "Registering scheduled task '$TaskName'"
    try {
        if ($task) { Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue }
        Register-ScheduledTask -TaskName $TaskName -Description 'Orchard personal library' `
            -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
        Start-ScheduledTask -TaskName $TaskName
    } catch {
        throw "Could not register the '$TaskName' task ($($_.Exception.Message)). If an Administrator set it up before, re-run this from an Administrator PowerShell."
    }

    if ($lan) {
        if ($isAdmin) {
            if (-not (Get-NetFirewallRule -DisplayName 'Orchard' -ErrorAction SilentlyContinue)) {
                New-NetFirewallRule -DisplayName 'Orchard' -Direction Inbound -Protocol TCP -LocalPort $Port `
                    -Action Allow -Profile Private, Domain | Out-Null
                Step "Allowed inbound TCP $Port through Windows Firewall on private and domain networks"
            }
        } else {
            Warn "Windows Firewall may block other devices; allow inbound TCP $Port, or re-run as Administrator"
        }
    }

    $healthy = $false
    foreach ($i in 1..10) {
        try {
            Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 -Uri "http://127.0.0.1:$Port/api/v1/health" | Out-Null
            $healthy = $true
            break
        } catch { Start-Sleep -Seconds 1 }
    }

    Write-Host ''
    if ($healthy) {
        Write-Host "Orchard is running. Open http://127.0.0.1:$Port"
        if ($lan) {
            $ip = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
                Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } |
                Select-Object -First 1 -ExpandProperty IPAddress
            if ($ip) { Write-Host "From other devices: http://${ip}:$Port" }
        }
        Write-Host 'On first visit, create the administrator account, then add a library.'
    } else {
        Warn "Orchard didn't answer on port $Port yet; check the task in Task Scheduler, or run '$bin' to see its output"
    }
    Write-Host ''
    Write-Host "Manage it with:  Start-ScheduledTask $TaskName  /  Stop-ScheduledTask $TaskName"
    Write-Host "Settings:        $launcher"
    Write-Host "Remove it with:  Unregister-ScheduledTask $TaskName"
}

Install-Orchard @args
