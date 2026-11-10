# Tests install.ps1's preflight judgement and duration format, whose functions are
# lifted out of the script since the script itself runs on load.
$ErrorActionPreference = 'Stop'
$script = Get-Content (Join-Path $PSScriptRoot '..\..\install.ps1') -Raw
function Lifted([string]$from, [string]$to) {
  $start = $script.IndexOf($from)
  $end = $script.IndexOf($to, $start)
  if ($start -lt 0 -or $end -lt 0) { throw "markers not found: $from" }
  $script.Substring($start, $end - $start)
}
Invoke-Expression (Lifted '  # Elapsed formats' '  # The phases of an install')
Invoke-Expression (Lifted '  # Processes that may hold lerd' '  # PortOwner names')
Invoke-Expression (Lifted '  # PreflightFindings turns' '  # ShowPreflight prints')
$env:SystemDrive = 'C:'

$failed = 0
function Expect($name, [bool]$ok, $detail) {
  if ($ok) { Write-Output "ok   $name" } else { Write-Output "FAIL $name`: $detail"; $script:failed++ }
}

function Facts($over) {
  $f = @{ Build = 26200; Virtualization = 'on'; FreeGB = 120; Online = @{ 'github.com' = $true; 'ghcr.io' = $true }; Ports = @{} }
  foreach ($k in $over.Keys) { $f[$k] = $over[$k] }
  $f
}
function Levels($findings) { ($findings | ForEach-Object { $_.Level }) -join ',' }
function Find($findings, $pattern) { $findings | Where-Object { $_.Text -match $pattern } | Select-Object -First 1 }

$all = PreflightFindings (Facts @{})
Expect 'a ready PC passes every check' (-not ($all | Where-Object { $_.Level -ne 'ok' })) (Levels $all)

$r = PreflightFindings (Facts @{ Build = 18363 })
Expect 'an old Windows stops the install' ((Find $r 'too old').Level -eq 'stop') (Levels $r)
$r = PreflightFindings (Facts @{ Build = 19045 })
Expect 'Windows 10 warns about .test in Windows browsers' ((Find $r 'mirrored').Level -eq 'warn') (Levels $r)

$r = PreflightFindings (Facts @{ Virtualization = 'off' })
Expect 'virtualization off stops the install' ((Find $r 'Virtualization').Level -eq 'stop') (Levels $r)
$r = PreflightFindings (Facts @{ Virtualization = 'unknown' })
Expect 'virtualization that cannot be read never blocks' (-not (Find $r 'Virtualization')) (Levels $r)

$r = PreflightFindings (Facts @{ FreeGB = 3 })
Expect 'under 5 GB free stops the install' ((Find $r 'GB free').Level -eq 'stop') (Levels $r)
$r = PreflightFindings (Facts @{ FreeGB = 10 })
Expect 'under 15 GB free warns' ((Find $r 'GB free').Level -eq 'warn') (Levels $r)
$r = PreflightFindings (Facts @{ FreeGB = -1 })
Expect 'free space that cannot be read never blocks' (-not (Find $r 'GB free')) (Levels $r)

$r = PreflightFindings (Facts @{ Online = @{ 'github.com' = $false; 'ghcr.io' = $false } })
Expect 'no internet stops the install' ((Find $r 'No connection').Level -eq 'stop') (Levels $r)
$r = PreflightFindings (Facts @{ Online = @{ 'github.com' = $true; 'ghcr.io' = $false } })
$c = Find $r 'No connection'
Expect 'one unreachable host warns and names it' ($c.Level -eq 'warn' -and $c.Text -match 'ghcr\.io' -and $c.Text -notmatch 'github') $c.Text

$r = PreflightFindings (Facts @{ Ports = @{ 80 = 'httpd' } })
$c = Find $r 'port 80'
Expect 'Apache on port 80 warns and names it' ($c.Level -eq 'warn' -and $c.Text -match 'httpd') (Levels $r)
Expect 'a port clash replaces the all-free line' (-not (Find $r 'are free')) (Levels $r)
$r = PreflightFindings (Facts @{ Ports = @{ 80 = 'lerd'; 443 = 'wslrelay' } })
Expect 'lerd and WSL on 80 and 443 are not a clash' ((Find $r 'are free').Level -eq 'ok') (Levels $r)
$r = PreflightFindings (Facts @{ Ports = @{ 7073 = 'node' } })
Expect 'another app on the dashboard port warns' ((Find $r '7073').Level -eq 'warn') (Levels $r)
$r = PreflightFindings (Facts @{ Ports = @{ 53 = 'nextdns' } })
Expect 'a DNS filter warns' ((Find $r 'DNS').Level -eq 'warn') (Levels $r)
$r = PreflightFindings (Facts @{ Ports = @{ 53 = 'lerd' } })
Expect "lerd's own DNS agent is not a warning" (-not (Find $r 'DNS')) (Levels $r)

Expect 'every stop and warn says what to do' (-not ((PreflightFindings (Facts @{ Build = 19045; Virtualization = 'off'; FreeGB = 3; Ports = @{ 80 = 'httpd'; 53 = 'x' } })) |
  Where-Object { $_.Level -ne 'ok' -and -not $_.Fix })) 'a finding without a fix'

foreach ($t in @(@(0, '0s'), @(42, '42s'), @(185, '3m 05s'), @(3600, '60m 00s'))) {
  $got = Elapsed ([TimeSpan]::FromSeconds($t[0]))
  Expect "Elapsed $($t[0])s reads $($t[1])" ($got -eq $t[1]) $got
}

if ($failed) { exit 1 }
