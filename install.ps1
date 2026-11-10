# Lerd installer for Windows — https://lerd.sh
# Usage, from a PowerShell window (no need to open it as administrator):
#   irm https://lerd.sh/install.ps1 | iex
#
# The console counterpart of Install-Lerd.exe (lerd-desktop's installer/), and it
# takes the same steps: it takes a Windows machine with nothing on it to a
# running lerd. The WSL features (and the restart they need), WSL itself, an
# Ubuntu distro with a Linux user, Podman, the Lerd desktop app, lerd's own
# install.sh inside the distro, then `lerd wsl:setup`, which wires lerd into
# Windows. Every step checks before it acts, so running it again after a failure
# resumes where it stopped. Keep the two in step.
#
# It runs as you, so the distro, the registry entries and the desktop app all
# belong to the signed-in user, and asks Windows for administrator rights only
# for the steps that need them: enabling the features, installing WSL, and
# putting the desktop app under Program Files.
#
# LERD_SETUP_LOCAL=<folder> names a Windows folder holding install.sh, lerd and
# lerd.exe, and optionally lerd-desktop-win32-<arch>.zip, to test a build before
# it is released.

# Everything runs inside one script block, so `irm | iex` leaves no variables or
# functions behind in the caller's session, and a failure returns to the prompt
# instead of closing the window the way `exit` would.
& {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

  $FallbackDistro = 'Ubuntu-26.04'
  $MinUbuntu = 2404
  $Distro = $FallbackDistro
  # lerd's installer asks sudo for its password, and the steps run detached from
  # the terminal, so the user gets passwordless sudo only while they run.
  $SudoersDropIn = '/etc/sudoers.d/zz-lerd-setup'
  $LxssKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss'
  $RunKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
  $RunOnceKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\RunOnce'
  # The AppUserModelID lerd's own toasts use, shared by the desktop app.
  $ToastAppID = 'sh.lerd.Lerd'
  $Repo = 'lerd-env/lerd'
  # Where Install-Lerd.exe installs too: wsl:setup leaves lerd's agent to a desktop
  # app started from Program Files, which Windows Defender lets be.
  $AppDir = Join-Path $env:ProgramW6432 'Lerd'
  $DesktopDir = Join-Path $AppDir 'desktop'
  $DesktopExe = Join-Path $DesktopDir 'lerd-desktop.exe'
  $DesktopVersionFile = Join-Path $AppDir 'desktop.version'
  $Local = $env:LERD_SETUP_LOCAL
  $env:WSL_UTF8 = '1'
  # What the steps learn and the answers they act on. A table, so the functions
  # below change it in place instead of reaching for $script: scope, which `irm
  # | iex` would make the caller's own session.
  $S = @{ LinuxUser = ''; Password = $null; UserExists = $false; LerdInstalled = $false; DNSLocalhost = $false; ManageNode = $true; MCP = $null; OptionsPending = $false; Phase = ''; DashboardUp = $false }
  # Everything this run says goes here too, for a failure to be looked into.
  $SetupLog = Join-Path $env:LOCALAPPDATA 'lerd\setup.log'
  # Choices wait here until applied, so a run cut off after install.sh still applies them.
  $OptionsFile = Join-Path $env:LOCALAPPDATA 'lerd\setup-options.json'

  # ---- output ----------------------------------------------------------------

  function Info($s) { Write-Host "  --> $s" -ForegroundColor Cyan }
  function Ok($s) { Write-Host "  OK  $s" -ForegroundColor Green }
  function Warn($s) { Write-Host "  !   $s" -ForegroundColor Yellow }
  function Step($s) { Write-Host "  ->  $s" -ForegroundColor White }

  # Elapsed formats a duration the way the progress lines show it: 42s, 3m 05s.
  function Elapsed([TimeSpan]$t) {
    if ($t.TotalSeconds -lt 60) { return ('{0}s' -f [int][math]::Floor($t.TotalSeconds)) }
    '{0}m {1:00}s' -f [int][math]::Floor($t.TotalMinutes), $t.Seconds
  }

  # The phases of an install, numbered on screen so a long one never looks stuck.
  $Phases = @(
    @{ Title = 'Installing WSL'; Time = 'about 2 minutes' }
    @{ Title = 'Installing Ubuntu'; Time = 'about 3 minutes' }
    @{ Title = 'Creating your Linux user'; Time = 'under a minute' }
    @{ Title = 'Installing Podman'; Time = 'about 2 minutes' }
    @{ Title = 'Installing the Lerd desktop app'; Time = 'under a minute' }
    @{ Title = 'Installing lerd and connecting it with Windows'; Time = '5 to 15 minutes the first time' }
    @{ Title = 'Starting lerd'; Time = 'about a minute' }
    @{ Title = 'Checking that everything works'; Time = 'a few seconds' }
  )

  # Phase runs one numbered phase, timing it. The title is kept so a failure can
  # say where the install stopped.
  function Phase([int]$n, [scriptblock]$work) {
    $p = $Phases[$n - 1]
    $S.Phase = $p.Title
    Write-Host ''
    Write-Host ('[{0}/{1}] {2}' -f $n, $Phases.Count, $p.Title) -ForegroundColor White -NoNewline
    Write-Host ('   {0}' -f $p.Time) -ForegroundColor DarkGray
    $t = [Diagnostics.Stopwatch]::StartNew()
    & $work
    Write-Host ('  OK  done in {0}' -f (Elapsed $t.Elapsed)) -ForegroundColor Green
  }

  # ---- running commands ------------------------------------------------------

  # RealWSLExe is where the WSL package installs wsl.exe. The one in System32 is
  # a stub until then, and asked anything it opens its own prompt offering to
  # install or update WSL.
  $RealWSLExe = Join-Path $env:ProgramW6432 'WSL\wsl.exe'
  function WSLExe {
    if (Test-Path $RealWSLExe) { $RealWSLExe } else { Join-Path $env:SystemRoot 'System32\wsl.exe' }
  }

  # Lit makes $s a single-quoted PowerShell literal, for paths written into the
  # scripts Elevated runs: a profile like O'Brien would otherwise end the string.
  function Lit([string]$s) { "'" + ($s -replace "'", "''") + "'" }

  # Sh makes $s a single-quoted literal for the Linux shell, where '' is no escape.
  function Sh([string]$s) { "'" + ($s -replace "'", "'\''") + "'" }

  # Quote escapes $s as one argument for wsl.exe's CommandLineToArgvW parsing;
  # PowerShell 5.1 passes embedded quotes unescaped, so lines are built by hand.
  function Quote([string]$s) {
    $r = '"'; $n = 0
    foreach ($c in $s.ToCharArray()) {
      if ($c -eq '\') { $n++ }
      else {
        if ($c -eq '"') { $r += '\' * ($n + 1) }
        $n = 0
      }
      $r += $c
    }
    $r + ('\' * $n) + '"'
  }

  # Detached runs a script in a new session with no controlling terminal: the
  # console gives WSL a pty, where a question read from /dev/tty would wait, and
  # without one every question takes its default.
  function Detached([string]$shell, [string]$body) {
    "--exec setsid -w $shell " + (Quote ("{`n" + $body + "`n} </dev/null"))
  }

  # --exec rather than --, which would hand the script to the user's shell first.
  function AsRoot([string]$body) { "-d $Distro -u root " + (Detached 'sh -c' $body) }
  function AsUser([string]$body) { "-d $Distro -u $($S.LinuxUser) --cd ~ " + (Detached 'bash -lc' $body) }

  function NewProcess([string]$exe, [string]$params) {
    $p = New-Object System.Diagnostics.ProcessStartInfo $exe, $params
    $p.UseShellExecute = $false
    $p
  }

  # Run runs a step's command with its output straight in the console, and throws
  # naming the step when it fails.
  function Run([string]$status, [string]$exe, [string]$params) {
    $proc = [Diagnostics.Process]::Start((NewProcess $exe $params))
    $proc.WaitForExit()
    if ($proc.ExitCode -ne 0) { throw "$status failed (exit code $($proc.ExitCode))" }
  }

  # Capture runs a command quietly and returns its exit code and trimmed output
  # lines. Stray NULs are dropped, in case wsl.exe writes UTF-16 regardless.
  function Capture([string]$exe, [string]$params) {
    $si = NewProcess $exe $params
    $si.RedirectStandardOutput = $true
    $si.RedirectStandardError = $true
    $si.StandardOutputEncoding = [Text.Encoding]::UTF8
    try { $proc = [Diagnostics.Process]::Start($si) } catch { return @{ Code = -1; Lines = @() } }
    $err = $proc.StandardError.ReadToEndAsync()
    $out = $proc.StandardOutput.ReadToEnd()
    $proc.WaitForExit()
    $null = $err.Result
    $lines = @($out -split "`r?`n" | ForEach-Object { $_.Replace("`0", '').Trim() } | Where-Object { $_ })
    @{ Code = $proc.ExitCode; Lines = $lines }
  }

  # Elevated runs a PowerShell script as administrator, which makes Windows ask,
  # and shows what it printed. It returns the script's exit code.
  function Elevated([string]$body) {
    $log = [IO.Path]::GetTempFileName()
    $wrapped = "`$ProgressPreference = 'SilentlyContinue'; `$ErrorActionPreference = 'Stop'; " +
      "try { & { $body } *>&1 | Out-File -Encoding utf8 $(Lit $log); exit 0 } " +
      "catch { `$_ | Out-File -Append -Encoding utf8 $(Lit $log); exit 1 }"
    $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($wrapped))
    try {
      $proc = Start-Process powershell.exe -Verb RunAs -Wait -PassThru -WindowStyle Hidden `
        -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', $enc
    } catch {
      throw 'Windows did not grant administrator rights'
    }
    Get-Content $log -ErrorAction SilentlyContinue | Where-Object { $_.Trim() } | ForEach-Object { Write-Host "      $_" }
    Remove-Item $log -ErrorAction SilentlyContinue
    $proc.ExitCode
  }

  function IsAdmin {
    ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
      [Security.Principal.WindowsBuiltInRole]::Administrator)
  }

  # NativeArch reads the machine's own architecture, which an x64 PowerShell
  # emulated on Arm64 does not report as its own.
  function NativeArch {
    $a = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment').PROCESSOR_ARCHITECTURE
    if ($a -eq 'ARM64') { 'arm64' } else { 'x64' }
  }

  # ---- state checks ----------------------------------------------------------

  function WSLFeaturesEnabled {
    foreach ($f in 'Microsoft-Windows-Subsystem-Linux', 'VirtualMachinePlatform') {
      # Get-WindowsOptionalFeature needs administrator rights; dism's English
      # output does not survive translation, so the CIM class answers instead.
      $s = (Get-CimInstance Win32_OptionalFeature -Filter "Name = '$f'").InstallState
      if ($s -ne 1) { return $false }
    }
    $true
  }

  # WSLInstalled reports whether `wsl --version` ends its first line in an
  # a.b.c.d version. Its labels are translated, and the inbox stub never prints one.
  function WSLInstalled {
    if (-not (Test-Path $RealWSLExe)) { return $false }
    $r = Capture $RealWSLExe '--version'
    ($r.Code -eq 0) -and ($r.Lines.Count -gt 0) -and ($r.Lines[0] -match ':\s*\d+\.\d+\.\d+\.\d+$')
  }

  function DistroInstalled {
    if (-not (Test-Path $RealWSLExe)) { return $false }
    $r = Capture $RealWSLExe '-l -q'
    ($r.Code -eq 0) -and ($r.Lines -contains $Distro)
  }

  # NewestUbuntu returns the newest Ubuntu-NN.NN in a `wsl` list. Lerd needs
  # podman 4.5, which Ubuntu ships from 24.04 on, so anything older does not count.
  function NewestUbuntu([string[]]$lines) {
    $lines | ForEach-Object { if ($_ -match '^(Ubuntu-(\d\d)\.(\d\d))(\s|$)') { $Matches[1] } } |
      Where-Object { [int]($_ -replace '\D') -ge $MinUbuntu } |
      Sort-Object { [int]($_ -replace '\D') } | Select-Object -Last 1
  }

  # Wired reads the distro or user Windows already runs Lerd with, from lerd-wsl.conf.
  function Wired([string]$key) {
    $conf = Join-Path $env:LOCALAPPDATA 'lerd\bin\lerd-wsl.conf'
    if (Test-Path $conf) {
      Get-Content $conf | Where-Object { $_ -like "$key=*" } | ForEach-Object { $_.Substring($key.Length + 1).Trim() }
    }
  }

  # InstalledDistro is the distro to stay on: the one Lerd runs from, or else the
  # newest usable Ubuntu. Moving to another would leave the user's sites behind.
  function InstalledDistro {
    if (-not (Test-Path $RealWSLExe)) { return $null }
    $r = Capture $RealWSLExe '-l -q'
    if ($r.Code -ne 0) { return $null }
    $wired = Wired 'distro'
    if ($wired -and ($r.Lines -contains $wired)) { return $wired }
    NewestUbuntu $r.Lines
  }

  # OfferedUbuntu is empty on a WSL too old to list a usable Ubuntu. A failed
  # lookup gives the fallback, so the install reports the network error itself.
  function OfferedUbuntu {
    $r = Capture $RealWSLExe '--list --online'
    if ($r.Code -eq 0) { NewestUbuntu $r.Lines } else { $FallbackDistro }
  }

  # PickDistro stays on a distro already here: a newer one on offer would
  # otherwise get a second, empty distro beside it.
  function PickDistro {
    $d = InstalledDistro
    if (-not $d) { $d = OfferedUbuntu }
    if ($d) { $d } else { $FallbackDistro }
  }

  function WSLReady { (WSLInstalled) -and ((InstalledDistro) -or (OfferedUbuntu)) }

  # ExistingUser returns the user Lerd runs as here, or else the distro's uid 1000
  # user, the one this script creates.
  function ExistingUser {
    $wired = if ($Distro -eq (Wired 'distro')) { Wired 'user' }
    foreach ($u in @($wired, 1000)) {
      if (-not $u) { continue }
      $r = Capture (WSLExe) "-d $Distro -u root --exec id -nu $u"
      if (($r.Code -eq 0) -and ($r.Lines.Count -gt 0)) { return $r.Lines[0] }
    }
    ''
  }

  # LerdInstalledFor reports whether lerd is already in the user's ~/.local/bin,
  # in which case it keeps the choices it settled on and nothing is asked again.
  function LerdInstalledFor([string]$user) {
    (Capture (WSLExe) ("-d $Distro -u $user --cd ~ " + (Detached 'sh -c' 'test -x ~/.local/bin/lerd'))).Code -eq 0
  }

  # LinuxUsername derives a valid Linux login from a Windows user name: lower
  # case, letters, digits and dashes, starting with a letter, at most 32 long.
  function LinuxUsername([string]$windows) {
    $u = ($windows.ToLowerInvariant() -replace '[^a-z0-9-]', '')
    if (-not $u) { $u = 'lerd' } elseif ($u[0] -notmatch '[a-z]') { $u = "u$u" }
    if ($u.Length -gt 32) { $u = $u.Substring(0, 32) }
    $u
  }

  $Reserved = 'root daemon bin sys sync games man lp mail news uucp proxy www-data backup list irc nobody systemd-network systemd-resolve messagebus syslog ubuntu' -split ' '

  # ---- preflight -------------------------------------------------------------

  # Processes that may hold lerd's ports because they are lerd, or WSL serving it.
  $OwnPortHolders = 'lerd', 'lerd-desktop', 'wslrelay', 'wslhost', 'wslservice', 'vmmem', 'vmmemWSL', 'vmcompute'

  # PortOwner names what listens on a port, for a message a person can act on.
  function PortOwner([int]$procId) {
    if ($procId -eq 4) { return 'Windows (HTTP.sys, usually IIS)' }
    $p = Get-Process -Id $procId -ErrorAction SilentlyContinue
    if ($p) { $p.ProcessName } else { "process $procId" }
  }

  # PreflightFacts reads what decides whether lerd can install here, before
  # anything changes. Each probe fails soft: a check that cannot run never blocks.
  function PreflightFacts {
    $f = @{ Build = [Environment]::OSVersion.Version.Build; Virtualization = 'unknown'; FreeGB = -1; Online = @{}; Ports = @{} }
    try {
      if ((Get-CimInstance Win32_ComputerSystem).HypervisorPresent) { $f.Virtualization = 'on' }
      else {
        $v = (Get-CimInstance Win32_Processor | Select-Object -First 1).VirtualizationFirmwareEnabled
        if ($v -eq $true) { $f.Virtualization = 'on' } elseif ($v -eq $false) { $f.Virtualization = 'off' }
      }
    } catch {}
    try { $f.FreeGB = [int][math]::Floor((Get-PSDrive $env:SystemDrive.TrimEnd(':')).Free / 1GB) } catch {}
    foreach ($h in 'github.com', 'ghcr.io') {
      $f.Online[$h] = $false
      try { $null = Invoke-WebRequest -UseBasicParsing -Method Head -TimeoutSec 8 "https://$h"; $f.Online[$h] = $true }
      catch { if ($_.Exception.Response) { $f.Online[$h] = $true } }
    }
    foreach ($port in 80, 443, 7073) {
      $c = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue | Select-Object -First 1
      if ($c) { $f.Ports[$port] = PortOwner $c.OwningProcess }
    }
    $u = Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in '127.0.0.1', '0.0.0.0' } | Select-Object -First 1
    if ($u) { $f.Ports[53] = PortOwner $u.OwningProcess }
    $f
  }

  # PreflightFindings turns the facts into one line per check: ok, warn (shown,
  # the install goes on) or stop (it cannot work until fixed), each stop and
  # warn with the fix. Pure, so the judgement is tested without a PC to probe.
  function PreflightFindings($f) {
    $out = New-Object System.Collections.ArrayList
    function Add($level, $text, $fix) { [void]$out.Add(@{ Level = $level; Text = $text; Fix = $fix }) }

    if ($f.Build -lt 19041) {
      Add 'stop' "Windows build $($f.Build) is too old for WSL 2" 'Update Windows in Settings > Windows Update to Windows 10 version 2004 or newer, then run the installer again.'
    } elseif ($f.Build -lt 22621) {
      Add 'warn' "Windows build $($f.Build) has no mirrored networking, so .test sites may not open in Windows browsers" 'Windows 11 22H2 or newer fixes this; lerd still installs.'
    } else { Add 'ok' "Windows build $($f.Build)" '' }

    switch ($f.Virtualization) {
      'off' { Add 'stop' "Virtualization is turned off in this PC's firmware, and WSL 2 needs it" 'Restart into the BIOS or UEFI setup, turn on Intel VT-x or AMD-V (SVM), then run the installer again.' }
      'on' { Add 'ok' 'Virtualization is on' '' }
    }

    if ($f.FreeGB -ge 0) {
      if ($f.FreeGB -lt 5) { Add 'stop' "Only $($f.FreeGB) GB free on $env:SystemDrive" 'Lerd needs about 10 GB for Ubuntu, Podman and PHP. Free some space, then run the installer again.' }
      elseif ($f.FreeGB -lt 15) { Add 'warn' "Only $($f.FreeGB) GB free on $env:SystemDrive" 'Lerd needs about 10 GB, and more as you add PHP versions and databases.' }
      else { Add 'ok' "$($f.FreeGB) GB free on $env:SystemDrive" '' }
    }

    $down = @($f.Online.Keys | Where-Object { -not $f.Online[$_] } | Sort-Object)
    if ($f.Online.Count -gt 0 -and $down.Count -eq $f.Online.Count) {
      Add 'stop' 'No connection to github.com or ghcr.io' 'Check your internet connection, or set your proxy in Settings > Network & internet > Proxy, then run the installer again.'
    } elseif ($down.Count -gt 0) {
      Add 'warn' "No connection to $($down -join ', ')" 'A firewall or proxy may be blocking it; downloads from there will fail.'
    } elseif ($f.Online.Count -gt 0) { Add 'ok' 'Internet connection' '' }

    $clash = $false
    foreach ($port in 80, 443) {
      $who = $f.Ports[$port]
      if ($who -and $OwnPortHolders -notcontains $who) {
        $clash = $true
        Add 'warn' "$who is using port $port, which lerd's sites need" 'Quit it before using lerd (for XAMPP, Laragon or WAMP, stop Apache in their control panel; for IIS, stop the World Wide Web Publishing Service), or lerd''s sites will not load.'
      }
    }
    if (-not $clash) { Add 'ok' 'Ports 80 and 443 are free for your sites' '' }
    $who = $f.Ports[7073]
    if ($who -and $OwnPortHolders -notcontains $who) {
      Add 'warn' "$who is using port 7073, which lerd's dashboard needs" 'Quit it, or the dashboard will not open.'
    }
    $who = $f.Ports[53]
    if ($who -and $OwnPortHolders -notcontains $who) {
      Add 'warn' "$who is answering DNS on this PC" 'A DNS filter such as NextDNS can keep .test sites from resolving in Windows; if they do not open, quit it or add your sites to the hosts file.'
    }
    , $out.ToArray()
  }

  # ShowPreflight prints the checks and reports whether the install can go on.
  function ShowPreflight($findings) {
    foreach ($c in $findings) {
      switch ($c.Level) {
        'ok' { Write-Host "  OK  $($c.Text)" -ForegroundColor Green }
        'warn' { Write-Host "  !   $($c.Text)" -ForegroundColor Yellow; Write-Host "      $($c.Fix)" -ForegroundColor DarkGray }
        'stop' { Write-Host "  X   $($c.Text)" -ForegroundColor Red; Write-Host "      $($c.Fix)" -ForegroundColor Gray }
      }
    }
    -not ($findings | Where-Object { $_.Level -eq 'stop' })
  }

  # ---- questions -------------------------------------------------------------

  function AskYes([string]$q, [bool]$default) {
    $hint = if ($default) { '[Y/n]' } else { '[y/N]' }
    $a = (Read-Host "  ?   $q $hint").Trim()
    if (-not $a) { return $default }
    $a -match '^[Yy]'
  }

  function Plain([Security.SecureString]$s) {
    $b = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($s)
    try { [Runtime.InteropServices.Marshal]::PtrToStringBSTR($b) } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($b) }
  }

  function AskUser {
    Write-Host ''
    Write-Host "  lerd runs inside an Ubuntu WSL distro, under its own Linux user."
    Write-Host "  Choose a name and a password for that user. sudo asks for this password inside Linux."
    $suggested = LinuxUsername $env:USERNAME
    while ($true) {
      $u = (Read-Host "  ?   Username [$suggested]").Trim()
      if (-not $u) { $u = $suggested }
      if ($u -cnotmatch '^[a-z][a-z0-9-]{0,31}$') {
        Warn 'The username must start with a lowercase letter and use only lowercase letters, digits and dashes, up to 32 characters.'
      } elseif ($Reserved -contains $u) {
        Warn "$u is already a system user in Ubuntu; choose another name."
      } else { break }
    }
    $S.LinuxUser = $u
    AskPassword
  }

  function AskPassword {
    while ($true) {
      $p1 = Plain (Read-Host '  ?   Password' -AsSecureString)
      if (-not $p1) { Warn 'Choose a password.'; continue }
      $p2 = Plain (Read-Host '  ?   Confirm password' -AsSecureString)
      if ($p1 -ceq $p2) { break }
      Warn 'The passwords do not match.'
    }
    $S.Password = $p1
  }

  # UserHasPassword reports whether $user has a usable password: an install
  # stopped between useradd and chpasswd leaves the user without one.
  function UserHasPassword([string]$user) {
    $r = Capture (WSLExe) "-d $Distro -u root --exec passwd -S $user"
    ($r.Code -eq 0) -and ($r.Lines.Count -gt 0) -and (($r.Lines[0] -split '\s+')[1] -eq 'P')
  }

  # AskOptions asks up front what lerd's installers would ask along the way,
  # where the steps have no terminal to show them.
  function AskOptions {
    Write-Host ''
    Write-Host '  Local domains:'
    Write-Host '    1) https://<name>.test with trusted certificates (recommended)'
    Write-Host '    2) http://<name>.localhost over plain HTTP, with no DNS or certificates'
    while ($true) {
      $a = (Read-Host '  ?   Choose [1]').Trim()
      if ($a -in '', '1') { $S.DNSLocalhost = $false; break }
      if ($a -eq '2') { $S.DNSLocalhost = $true; break }
    }
    Write-Host ''
    Write-Host '  lerd can install Node.js per project and put node, npm and npx shims on PATH.'
    $S.ManageNode = AskYes 'Let lerd manage Node.js versions?' $true
    Write-Host ''
    Write-Host '  AI assistants such as Claude Code, Cursor or Codex can manage lerd through its MCP server.'
    $S.MCP = AskYes 'Let your AI assistants use lerd through MCP?' $true
    Write-Host '  Each can be changed later, from the dashboard or the lerd command.'
  }

  # ---- steps -----------------------------------------------------------------

  # EnableFeatures turns on the features WSL 2 runs on. They only take effect
  # after a restart, so the script registers itself to run again at the next
  # sign-in and stops here.
  function EnableFeatures {
    Step 'Enabling the WSL features...'
    if ((Elevated 'Enable-WindowsOptionalFeature -Online -NoRestart -All -FeatureName Microsoft-Windows-Subsystem-Linux, VirtualMachinePlatform | Out-Null') -ne 0) {
      throw 'Enabling the WSL features failed'
    }
    if ($PSCommandPath) {
      $resume = "& '$($PSCommandPath.Replace("'", "''"))'"
    } else {
      $resume = 'irm https://lerd.sh/install.ps1 | iex'
    }
    if ($Local) { $resume = "`$env:LERD_SETUP_LOCAL = '$($Local.Replace("'", "''"))'; $resume" }
    $resume = "`$env:LERD_SETUP_RESUMED = '1'; $resume"
    New-Item -Path $RunOnceKey -Force | Out-Null
    Set-ItemProperty -Path $RunOnceKey -Name 'lerd-setup' -Value ("powershell.exe -NoProfile -ExecutionPolicy Bypass -NoExit -Command " + (Quote $resume))
    Write-Host ''
    Write-Host '  Windows needs a restart to finish enabling WSL. The installer carries on by itself after you sign back in.'
    if (AskYes 'Restart now?' $true) { Restart-Computer -Force }
  }

  # EnsureWSL installs WSL from Microsoft's GitHub release. The wsl.exe Windows
  # ships is a stub, and its Store-based install is unreliable.
  function EnsureWSL {
    if (WSLReady) { return }
    Step 'Installing WSL...'
    $arch = NativeArch
    $url = (Invoke-RestMethod https://api.github.com/repos/microsoft/WSL/releases/latest).assets |
      Where-Object { $_.name -like "*.$arch.msi" } | Select-Object -First 1 -ExpandProperty browser_download_url
    if (-not $url) { throw "No WSL $arch MSI found in the latest github.com/microsoft/WSL release" }
    # The elevated step downloads the package itself, into a folder only
    # administrators can change, and checks Microsoft signed it: a file in the
    # user's temp folder could be swapped while Windows asks for approval.
    Info "Downloading and installing $url"
    $code = Elevated ("`$d = New-Item -ItemType Directory -Path (Join-Path `$env:SystemRoot ('Temp\lerd-wsl-' + [guid]::NewGuid())); " +
      "`$m = Join-Path `$d 'wsl.msi'; Invoke-WebRequest -UseBasicParsing $(Lit $url) -OutFile `$m; " +
      "`$sig = Get-AuthenticodeSignature `$m; " +
      "if (`$sig.Status -ne 'Valid' -or `$sig.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') { throw 'The WSL package is not signed by Microsoft' }; " +
      "`$p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList '/i', ('`"' + `$m + '`"'), '/qn', '/norestart'; " +
      "Remove-Item `$d -Recurse -Force -ErrorAction SilentlyContinue; " +
      "if (`$p.ExitCode -notin 0, 3010) { throw ""msiexec exited with `$(`$p.ExitCode)"" }")
    if ($code -ne 0) { throw 'Installing WSL failed' }
    # msiexec returns before Windows Installer finishes registering WSL, and the
    # stub asked in that window offers to install WSL all over again.
    for ($i = 0; $i -lt 60; $i++) {
      if (WSLReady) { return }
      Start-Sleep 2
    }
    throw 'WSL was installed but wsl.exe still does not report it'
  }

  function EnsureDistro {
    if (DistroInstalled) { return }
    Step "Installing $Distro..."
    Run "Installing $Distro" (WSLExe) "--install -d $Distro --no-launch --web-download"
  }

  # MarkProvisioned makes uid 1000 the distro's default user and tells WSL its
  # first-run setup is done, so the distro never asks for a user of its own.
  function MarkProvisioned([int]$uid) {
    Get-ChildItem $LxssKey -ErrorAction SilentlyContinue | ForEach-Object {
      if ((Get-ItemProperty $_.PSPath).DistributionName -eq $Distro) {
        Set-ItemProperty $_.PSPath -Name DefaultUid -Value $uid -Type DWord
        Set-ItemProperty $_.PSPath -Name RunOOBE -Value 0 -Type DWord
      }
    }
  }

  # EnsureUser gives the distro a default sudo user and systemd. The password
  # reaches chpasswd through WSLENV, never a command line or the disk, and
  # --terminate restarts the distro for the wsl.conf settings.
  function EnsureUser {
    $status = "Creating the Linux user $($S.LinuxUser)"
    Step "$status..."
    if (-not $S.UserExists) {
      Run $status (WSLExe) (AsRoot "useradd -m -s /bin/bash -G sudo $($S.LinuxUser)")
      $S.UserExists = $true
    }
    if ($S.Password) {
      $wslenv = $env:WSLENV
      $env:LERD_SETUP_PW = $S.Password
      $env:WSLENV = if ($wslenv) { "${wslenv}:LERD_SETUP_PW" } else { 'LERD_SETUP_PW' }
      try {
        Run $status (WSLExe) (AsRoot "printf '%s:%s\n' $($S.LinuxUser) `"`$LERD_SETUP_PW`" | chpasswd")
      } finally {
        Remove-Item Env:LERD_SETUP_PW -ErrorAction SilentlyContinue
        $env:WSLENV = $wslenv
        $S.Password = $null
      }
    }
    Run $status (WSLExe) (AsRoot ("f=/etc/wsl.conf; touch `$f`n" +
        "grep -q '^systemd=true' `$f || printf '\n[boot]\nsystemd=true\n' >> `$f`n" +
        "grep -q '^default=' `$f || printf '\n[user]\ndefault=$($S.LinuxUser)\n' >> `$f"))
    $uid = (Capture (WSLExe) "-d $Distro -u root --exec id -u $($S.LinuxUser)").Lines | Select-Object -First 1
    if (-not $uid) { $uid = 1000 }
    Run $status (WSLExe) "--terminate $Distro"
    MarkProvisioned $uid
  }

  # InstallPackages installs the packages lerd's installer checks for, and turns
  # on linger, because with no terminal its offer to do both reads as "no" and it
  # stops without Podman. Keep the list in step with install.sh and the WSL guide.
  function InstallPackages {
    Step 'Installing Podman and the packages lerd needs...'
    Run 'Installing the packages' (WSLExe) (AsRoot ("export DEBIAN_FRONTEND=noninteractive`n" +
        '{ ! command -v apt-get >/dev/null || { apt-get update -q && apt-get install -y -q podman uidmap fuse-overlayfs slirp4netns curl git unzip libnss3-tools; }; } && ' +
        "loginctl enable-linger $($S.LinuxUser)"))
  }

  # ---- desktop app -----------------------------------------------------------

  # The desktop app holds lerd's tray and notifications on Windows and keeps its
  # agent running. It has no installer of its own, so it is downloaded from the
  # latest lerd-desktop release and registered here.

  # SetShortcutAppID stamps the app id on a shortcut, which ties it to the app's
  # own windows and notifications. WScript.Shell cannot set it.
  function SetShortcutAppID([string]$lnk, [string]$id) {
    if (-not ('LerdSetup.Lnk' -as [type])) {
      Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
namespace LerdSetup {
  [StructLayout(LayoutKind.Sequential, Pack = 4)]
  public struct PropertyKey { public Guid fmtid; public uint pid; }
  [StructLayout(LayoutKind.Explicit, Size = 16)]
  public struct PropVariant { [FieldOffset(0)] public ushort vt; [FieldOffset(8)] public IntPtr p; }
  [ComImport, Guid("886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IPropertyStore {
    void GetCount(out uint c);
    void GetAt(uint i, out PropertyKey k);
    void GetValue(ref PropertyKey k, out PropVariant v);
    void SetValue(ref PropertyKey k, ref PropVariant v);
    void Commit();
  }
  [ComImport, Guid("00021401-0000-0000-C000-000000000046")]
  class CShellLink {}
  public static class Lnk {
    public static void SetAppID(string path, string id) {
      object link = new CShellLink();
      IPersistFile file = (IPersistFile)link;
      file.Load(path, 2);
      PropertyKey key = new PropertyKey { fmtid = new Guid("9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3"), pid = 5 };
      PropVariant v = new PropVariant { vt = 31, p = Marshal.StringToCoTaskMemUni(id) };
      try {
        IPropertyStore store = (IPropertyStore)link;
        store.SetValue(ref key, ref v);
        store.Commit();
        file.Save(path, true);
      } finally {
        Marshal.FreeCoTaskMem(v.p);
      }
    }
  }
}
'@
    }
    [LerdSetup.Lnk]::SetAppID($lnk, $id)
  }

  # RegisterDesktopApp registers the app as lerd's: the app id its notifications
  # post under, the lerd:// scheme their clicks open, a Start menu shortcut, and
  # a hidden start at sign-in.
  function RegisterDesktopApp {
    $exe = "`"$DesktopExe`""
    $key = "HKCU:\Software\Classes\AppUserModelId\$ToastAppID"
    New-Item $key -Force | Out-Null
    Set-ItemProperty $key -Name DisplayName -Value 'Lerd'
    Set-ItemProperty $key -Name IconUri -Value (Join-Path $AppDir 'lerd.png')
    New-Item 'HKCU:\Software\Classes\lerd\DefaultIcon' -Force | Out-Null
    New-Item 'HKCU:\Software\Classes\lerd\shell\open\command' -Force | Out-Null
    Set-Item 'HKCU:\Software\Classes\lerd' -Value 'URL:Lerd'
    Set-ItemProperty 'HKCU:\Software\Classes\lerd' -Name 'URL Protocol' -Value ''
    Set-Item 'HKCU:\Software\Classes\lerd\DefaultIcon' -Value "$exe,0"
    Set-Item 'HKCU:\Software\Classes\lerd\shell\open\command' -Value "$exe `"%1`""
    Set-ItemProperty $RunKey -Name 'lerd-desktop' -Value "$exe --hidden"

    $lnk = Join-Path ([Environment]::GetFolderPath('Programs')) 'Lerd.lnk'
    $sc = (New-Object -ComObject WScript.Shell).CreateShortcut($lnk)
    $sc.TargetPath = $DesktopExe
    $sc.WorkingDirectory = $DesktopDir
    $sc.Save()
    try { SetShortcutAppID $lnk $ToastAppID } catch { Warn "Could not set the shortcut's app id: $_" }
  }

  # StopDesktopApp ends a running copy of the app, which holds its files open.
  function StopDesktopApp {
    Get-Process -Name lerd-desktop -ErrorAction SilentlyContinue |
      Where-Object { $_.Path -like "$DesktopDir\*" } | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep 1
  }

  # InstallDesktopApp unpacks the app under Program Files before wsl:setup, which
  # then leaves the agent to it. LERD_SETUP_LOCAL's lerd-desktop-win32-<arch>.zip,
  # when there, is used instead and unpacked on every run.
  function InstallDesktopApp {
    Step 'Installing the Lerd desktop app...'
    $arch = NativeArch
    $zipName = "lerd-desktop-win32-$arch.zip"
    $tag = ''
    $zipUrl = ''
    if ($Local -and (Test-Path (Join-Path $Local $zipName))) {
      $zip = Join-Path $Local $zipName
      Info "Using $zip"
    } else {
      $r = Invoke-RestMethod https://api.github.com/repos/lerd-env/lerd-desktop/releases/latest
      $a = $r.assets | Where-Object { $_.name -eq $zipName } | Select-Object -First 1
      if (-not $a) { throw "No $zipName in the latest github.com/lerd-env/lerd-desktop release" }
      $tag = $r.tag_name
      if ((Test-Path $DesktopExe) -and (Test-Path $DesktopVersionFile) -and
        ((Get-Content $DesktopVersionFile -Raw).Trim() -eq $tag)) {
        Info "The desktop app is already at $tag"
        RegisterDesktopApp
        return
      }
      $zipUrl = $a.browser_download_url
      Info "Downloading $zipUrl"
    }
    # The logo Windows shows on lerd's notifications.
    $pngUrl = "https://raw.githubusercontent.com/$Repo/main/internal/ui/icons/icon-512.png"

    StopDesktopApp
    Info 'Unpacking the desktop app'
    # Windows' own tar unpacks the zip, its complaints shown since a half-unpacked
    # app opens black. Errors Continue around it, or PowerShell 5.1 turns its
    # first stderr line into a terminating error before the exit code is read.
    $tar = Join-Path $env:SystemRoot 'System32\tar.exe'
    # A released app is downloaded by the elevated step itself, so what lands
    # in Program Files is never a file the user's other programs could swap.
    $getZip = if ($zipUrl) {
      "`$t = New-Item -ItemType Directory -Path (Join-Path `$env:SystemRoot ('Temp\lerd-desktop-' + [guid]::NewGuid())); " +
      "`$z = Join-Path `$t 'app.zip'; Invoke-WebRequest -UseBasicParsing $(Lit $zipUrl) -OutFile `$z; "
    } else { "`$z = $(Lit $zip); " }
    $code = Elevated ("`$d = $(Lit $DesktopDir); " + $getZip +
      "if (Test-Path `$d) { Remove-Item `$d -Recurse -Force }; " +
      "Remove-Item $(Lit $DesktopVersionFile) -ErrorAction SilentlyContinue; " +
      "New-Item -ItemType Directory -Force `$d | Out-Null; " +
      "Invoke-WebRequest -UseBasicParsing $(Lit $pngUrl) -OutFile $(Lit (Join-Path $AppDir 'lerd.png')); " +
      "`$ErrorActionPreference = 'Continue'; & $(Lit $tar) -xf `$z -C `$d 2>&1 | ForEach-Object { ""`$_"" }; " +
      "if (`$LASTEXITCODE -ne 0) { throw ""tar exited with `$LASTEXITCODE"" }; " +
      "if (`$t) { Remove-Item `$t -Recurse -Force -ErrorAction SilentlyContinue }; " +
      "if ($(Lit $tag)) { Set-Content -Path $(Lit $DesktopVersionFile) -Value $(Lit $tag) -NoNewline }")
    if (($code -ne 0) -or -not (Test-Path $DesktopExe)) { throw 'Installing the Lerd desktop app failed' }
    RegisterDesktopApp
  }

  # StartDesktopApp starts the app as the signed-in user, never with
  # administrator rights, even from an elevated window.
  function StartDesktopApp([string]$arguments) {
    if (IsAdmin) {
      Start-Process "$env:SystemRoot\System32\runas.exe" -WindowStyle Hidden -ArgumentList '/trustlevel:0x20000', (Quote "`"$DesktopExe`" $arguments")
    } elseif ($arguments) {
      Start-Process $DesktopExe -ArgumentList $arguments
    } else {
      Start-Process $DesktopExe
    }
  }

  # ---- lerd ------------------------------------------------------------------

  function GrantSudo {
    $r = Capture (WSLExe) (AsRoot "printf '%s ALL=(ALL) NOPASSWD:ALL\n' $($S.LinuxUser) > $SudoersDropIn && chmod 0440 $SudoersDropIn")
    if ($r.Code -ne 0) { throw 'sudo failed (exit code 1)' }
  }

  function RemoveSudoers {
    $null = Capture (WSLExe) (AsRoot "rm -f $SudoersDropIn")
  }

  # ApplyOptions turns answers that differ from the installers' defaults into lerd
  # commands, before wsl:setup reads the DNS mode to decide on the .test rule.
  function ApplyOptions {
    if (-not $S.OptionsPending) { return }
    if ($S.DNSLocalhost) {
      Step 'Applying your options...'
      Run 'Applying your options' (WSLExe) (AsUser '~/.local/bin/lerd dns:disable')
    }
    if (-not $S.ManageNode) {
      Step 'Applying your options...'
      Run 'Applying your options' (WSLExe) (AsUser '~/.local/bin/lerd node:unmanage')
    }
    # disable-global on a no records it, so lerd update does not ask again.
    if ($null -ne $S.MCP) {
      Step 'Applying your options...'
      $mcp = if ($S.MCP) { 'mcp:enable-global' } else { 'mcp:disable-global' }
      Run 'Applying your options' (WSLExe) (AsUser "~/.local/bin/lerd $mcp")
    }
    Remove-Item $OptionsFile -ErrorAction SilentlyContinue
  }

  # TrustCA imports lerd's root CA from here, so Windows' confirmation opens in
  # front rather than behind, as wsl:setup's hidden certutil's can. Declining is
  # not a failure: the sites work, with a browser warning.
  function TrustCA {
    if ($S.OptionsPending -and $S.DNSLocalhost) { return }
    $crt = Join-Path $env:TEMP 'lerd-rootCA.crt'
    # No CA means .localhost mode, which has no certificates to trust.
    $r = Capture (WSLExe) (AsUser "cp `"`$(~/.local/share/lerd/bin/mkcert -CAROOT)/rootCA.pem`" `"`$(wslpath $(Sh $crt))`"")
    if ($r.Code -ne 0) { return }
    try {
      $c = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 $crt
      if (Test-Path "Cert:\CurrentUser\Root\$($c.Thumbprint)") { return }
      Step "Trusting lerd's certificate in Windows..."
      Write-Host "  Windows will now ask whether to install lerd's certificate authority."
      Write-Host '  Choose Yes, so your browsers trust your https://<name>.test sites.'
      $p = Start-Process "$env:SystemRoot\System32\certutil.exe" -Wait -PassThru -WindowStyle Hidden -ArgumentList '-addstore', '-user', '-f', 'Root', (Quote $crt)
      if ($p.ExitCode -ne 0) {
        Warn 'The certificate was not installed, so browsers will warn on your https sites. Run lerd wsl:setup inside WSL to be asked again.'
      }
    } finally {
      Remove-Item $crt -ErrorAction SilentlyContinue
    }
  }

  # InstallLerd runs lerd's own installer inside the distro, so Windows gets
  # what every Linux machine gets.
  function InstallLerd {
    # The MCP question was asked up front, so lerd install must not stop to ask it.
    $asked = if ($null -ne $S.MCP) { 'LERD_MCP_ASKED=1 ' } else { '' }
    if ($Local) {
      $body = "cd `"`$(wslpath $(Sh $Local))`" && ${asked}bash install.sh --local ./lerd"
    } else {
      $body = "curl -fsSL https://lerd.sh/install.sh | ${asked}bash"
    }
    Step "Installing lerd inside $Distro..."
    Run 'Installing lerd' (WSLExe) (AsUser $body)
    ApplyOptions
    TrustCA
    Step 'Connecting lerd with Windows...'
    Run 'Connecting lerd with Windows' (WSLExe) (AsUser 'LERD_WSL_RESTART_HANDLED=1 ~/.local/bin/lerd wsl:setup')
    # The desktop app only receives lerd's notifications from the native sink;
    # the browser sink, lerd's default, sends them to dashboard pages.
    Step "Handing lerd's notifications to the desktop app..."
    Run 'Handing over notifications' (WSLExe) (AsUser '~/.local/bin/lerd notify target native')
  }

  function DashboardUp {
    try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 http://127.0.0.1:7073/ | Out-Null; $true }
    catch { [bool]$_.Exception.Response }
  }

  function WaitForDashboard([int]$seconds) {
    $end = (Get-Date).AddSeconds($seconds)
    while ((Get-Date) -lt $end) {
      if (DashboardUp) { return $true }
      Start-Sleep 2
    }
    $false
  }

  # StartLerd restarts WSL so mirrored networking and the resolver handover take
  # effect, boots the distro again, and waits for the dashboard. One that stays
  # down is restarted once; the checks after say so if it still is.
  function StartLerd {
    Step 'Restarting WSL, so its new network settings take effect'
    Run 'Starting lerd' (WSLExe) '--shutdown'
    Run 'Starting lerd' (WSLExe) "-d $Distro --exec /bin/true"
    Step 'Waiting for the dashboard'
    $S.DashboardUp = WaitForDashboard 120
    if (-not $S.DashboardUp) {
      Step 'The dashboard is slow to start, restarting it'
      $null = Capture (WSLExe) (AsUser 'export XDG_RUNTIME_DIR=/run/user/$(id -u); systemctl --user reset-failed lerd-ui; systemctl --user restart lerd-ui')
      $S.DashboardUp = WaitForDashboard 90
    }
  }

  # ---- checks and finish -----------------------------------------------------

  # Verify checks what a user does first, from Windows: the dashboard, a .test
  # name, HTTPS trust, the commands on PATH, and what lerd doctor makes of it.
  function Verify {
    $checks = New-Object System.Collections.ArrayList
    function Check($ok, $text, $fix) { [void]$checks.Add(@{ Level = $(if ($ok) { 'ok' } else { 'warn' }); Text = $text; Fix = $fix }) }

    Check $S.DashboardUp 'The dashboard answers' 'Run lerd start in a new terminal, then open http://127.0.0.1:7073.'
    if (-not $S.DNSLocalhost) {
      $tld = Wired 'tld'
      if (-not $tld) { $tld = 'test' }
      $resolved = try { [Net.Dns]::GetHostAddresses("lerd-setup-check.$tld") | Where-Object { "$_" -in '127.0.0.1', '::1' } } catch { $null }
      Check ([bool]$resolved) ".$tld sites resolve in Windows" 'Open System > Troubleshoot in the dashboard, or run lerd doctor --fix.'
      $ca = Get-ChildItem Cert:\CurrentUser\Root -ErrorAction SilentlyContinue | Where-Object { $_.Subject -like '*mkcert*' }
      Check ([bool]$ca) 'Your browsers trust lerd''s HTTPS certificates' 'Run lerd wsl:setup in Ubuntu and answer Yes when Windows asks to install the certificate.'
    }
    $bin = Join-Path $env:LOCALAPPDATA 'lerd\bin'
    $onPath = ([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') -contains $bin
    $cmds = (Test-Path (Join-Path $bin 'lerd.exe')) -and (Test-Path (Join-Path $bin 'php.exe'))
    Check ($onPath -and $cmds) 'lerd, php and composer are on your Windows PATH' 'Run lerd wsl:setup in Ubuntu.'
    # An interactive shell, so the doctor sees the PATH a terminal has, lerd's shims included.
    $r = Capture (WSLExe) ("-d $Distro -u $($S.LinuxUser) --cd ~ " + (Detached 'bash -ic' '~/.local/bin/lerd doctor --json'))
    $rep = try { ($r.Lines -join "`n") | ConvertFrom-Json } catch { $null }
    if ($rep) {
      # A newer release is news, not a problem with this install.
      $n = @($rep.findings | Where-Object { $_.status -in 'fail', 'warn' -and $_.section -ne 'Version Info' }).Count
      Check ($n -eq 0) $(if ($n -eq 0) { 'lerd doctor finds nothing to fix' } else { "lerd doctor found $n thing(s) to look at" }) 'Open System > Troubleshoot in the dashboard: most have a Fix button.'
    }
    , $checks.ToArray()
  }

  # Finish is the last screen: whether lerd is ready, and the handful of things
  # a new user wants next, where to click and what to type.
  function Finish($checks, [TimeSpan]$took) {
    $issues = @($checks | Where-Object { $_.Level -ne 'ok' }).Count
    $sitesDir = "\\wsl.localhost\$Distro\home\$($S.LinuxUser)\Lerd"
    $site = if ($S.DNSLocalhost) { 'http://my-app.localhost' } else { 'https://my-app.test' }
    Write-Host ''
    if ($issues -eq 0) {
      Write-Host ("  Lerd is ready, set up in {0}." -f (Elapsed $took)) -ForegroundColor Green
    } else {
      Write-Host ("  Lerd is installed, set up in {0}. {1} thing(s) above need a look." -f (Elapsed $took), $issues) -ForegroundColor Yellow
    }
    Write-Host ''
    Write-Host '  Dashboard     http://lerd.localhost, or the Lerd icon in your tray'
    Write-Host "  Your sites    $sitesDir  (pinned in Explorer's Quick access)"
    Write-Host "  First site    open $Distro from the Start menu, then:  cd ~/Lerd && lerd new my-app"
    Write-Host "                and open $site in your browser"
    Write-Host '  From Windows  open a new terminal: lerd, php and composer work there too'
    Write-Host '  Need help?    System > Troubleshoot in the dashboard, or lerd doctor --fix'
    Write-Host "  Setup log     $SetupLog"
  }

  # ---- main ------------------------------------------------------------------

  $null = New-Item -ItemType Directory -Force (Split-Path $SetupLog)
  try { Start-Transcript -Path $SetupLog -Append | Out-Null } catch {}
  $Total = [Diagnostics.Stopwatch]::StartNew()
  # Set by the command that resumes the install after the restart WSL needs.
  $Resumed = $env:LERD_SETUP_RESUMED -eq '1'
  $Repair = [bool](Wired 'distro')
  try {

    Write-Host ''
    Write-Host 'Lerd installer for Windows' -ForegroundColor White
    Write-Host ''
    if ($Resumed) {
      Write-Host '  Welcome back. WSL is on now, so the install carries on where it left off.'
    } elseif ($Repair) {
      Write-Host '  Lerd is already set up on this PC. This run checks every part and repairs what has'
      Write-Host '  drifted; your sites, databases and settings stay as they are.'
    } else {
      Write-Host '  This sets up everything lerd needs, then connects it with Windows:'
      Write-Host '    WSL 2 and Ubuntu, Podman, lerd itself and the Lerd desktop app,'
      Write-Host '    .test domains with trusted HTTPS, and lerd, php and composer on your PATH.'
      Write-Host ''
      Write-Host '  It takes 15 to 30 minutes, mostly downloads. Windows may restart once and will ask you'
      Write-Host '  to approve administrator access a few times. A few questions come first; after them'
      Write-Host '  you can leave it running.'
    }
    Write-Host ''
    Write-Host '  Checking this PC...' -ForegroundColor White
    if (-not (ShowPreflight (PreflightFindings (PreflightFacts)))) {
      Write-Host ''
      Write-Host '  Nothing was changed. Fix what is marked X above, then run the installer again.' -ForegroundColor Red
      return
    }
    Remove-ItemProperty -Path $RunOnceKey -Name 'lerd-setup' -ErrorAction SilentlyContinue
    if (-not $Resumed -and -not $Repair) {
      Write-Host ''
      if (-not (AskYes 'Ready to begin?' $true)) {
        Write-Host '  Nothing was changed. Run the installer again whenever you are ready.'
        return
      }
    }

    $Distro = PickDistro

    # The user is asked for after the restart, so it is not typed twice.
    if (-not (WSLFeaturesEnabled)) {
      try { EnableFeatures } catch { Warn $_.Exception.Message; Warn 'Run the installer again to retry.' }
      return
    }

    if ((WSLInstalled) -and (DistroInstalled)) { $S.LinuxUser = ExistingUser }
    $S.UserExists = [bool]$S.LinuxUser
    $S.LerdInstalled = $S.UserExists -and (LerdInstalledFor $S.LinuxUser)

    if (-not $S.UserExists) {
      AskUser
    } elseif (-not (UserHasPassword $S.LinuxUser)) {
      Info "The Linux user $($S.LinuxUser) has no password yet; choose one, sudo asks for it inside Linux."
      AskPassword
    } else {
      Info "Using the existing Linux user $($S.LinuxUser)"
    }
    if (Test-Path $OptionsFile) {
      $o = Get-Content $OptionsFile -Raw | ConvertFrom-Json
      $S.DNSLocalhost = [bool]$o.DNSLocalhost
      $S.ManageNode = [bool]$o.ManageNode
      # Absent from a file an older installer saved, which never asked it.
      if ($null -ne $o.MCP) { $S.MCP = [bool]$o.MCP }
      $S.OptionsPending = $true
    } elseif (-not $S.LerdInstalled) {
      AskOptions
      $null = New-Item -ItemType Directory -Force (Split-Path $OptionsFile)
      @{ DNSLocalhost = $S.DNSLocalhost; ManageNode = $S.ManageNode; MCP = $S.MCP } | ConvertTo-Json | Set-Content $OptionsFile
      $S.OptionsPending = $true
    }

    Write-Host ''
    Write-Host '  That is everything to ask. The rest runs by itself; approve Windows'' prompts when they appear.' -ForegroundColor White

    try {
      Phase 1 { EnsureWSL }
      $Distro = PickDistro
      Phase 2 { EnsureDistro }
      Phase 3 { EnsureUser }
      Phase 4 { InstallPackages }
      Phase 5 { InstallDesktopApp }
      Phase 6 {
        Info 'The first install downloads lerd''s containers and prepares PHP, which can take a while; its progress shows below.'
        GrantSudo
        try { InstallLerd } finally { RemoveSudoers }
      }
      Phase 7 { StartLerd; StartDesktopApp '--hidden' }
    } catch {
      Write-Host ''
      Write-Host "  The install stopped while: $($S.Phase)" -ForegroundColor Red
      Write-Host "  Why: $($_.Exception.Message)" -ForegroundColor Red
      Write-Host ''
      Write-Host '  Run the installer again. It skips everything already done and picks up from here.'
      Write-Host "  If it stops again, the setup log at $SetupLog says more;"
      Write-Host "  attach it to a report at https://github.com/$Repo/issues."
      return
    }
    Phase 8 { $S.Checks = Verify; $null = ShowPreflight $S.Checks }

    Finish $S.Checks $Total.Elapsed
    Write-Host ''
    Write-Host "  *  If lerd is useful to you, a GitHub star helps others find it:" -ForegroundColor Cyan
    Write-Host "     https://github.com/$Repo"
    Write-Host ''
    if (AskYes 'Open Lerd now?' $true) { StartDesktopApp '' }

  } finally {
    try { Stop-Transcript | Out-Null } catch {}
  }
}
