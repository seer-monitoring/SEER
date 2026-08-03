# Live smoke test for seer-cli against a real Seer API.
#
# Usage (PowerShell):
#   $env:SEER_API_KEY = "your_key"
#   $env:SEER_BASE_URL = "https://api.ansrstudio.com"   # optional
#   $env:SEER_JOB_NAME = "your_dashboard_job_name"
#   pwsh -File cli/examples/live_smoke_test.ps1

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$CliDir = Split-Path -Parent $ScriptDir

function Import-DotEnv([string]$Path) {
    if (-not (Test-Path $Path)) { return }
    Write-Host "Loading $Path"
    Get-Content $Path | ForEach-Object {
        $line = $_.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { return }
        $eq = $line.IndexOf("=")
        if ($eq -lt 1) { return }
        $name = $line.Substring(0, $eq).Trim()
        $value = $line.Substring($eq + 1).Trim()
        if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
        Set-Item -Path "Env:$name" -Value $value
    }
}

Import-DotEnv (Join-Path $CliDir ".env")
Import-DotEnv (Join-Path (Split-Path -Parent $CliDir) ".env")

function Section([string]$Title) {
    Write-Host ""
    Write-Host ("=" * 60)
    Write-Host $Title
    Write-Host ("=" * 60)
}

if ([string]::IsNullOrWhiteSpace($env:SEER_API_KEY)) {
    Write-Host "Set SEER_API_KEY before running this script."
    Write-Host '  $env:SEER_API_KEY = "seer_..."'
    Write-Host "Or create cli/.env with:"
    Write-Host "  SEER_API_KEY=..."
    Write-Host "  SEER_JOB_NAME=a"
    exit 1
}

$baseUrl = if ($env:SEER_BASE_URL) { $env:SEER_BASE_URL } else { "https://api.ansrstudio.com" }
$jobName = if ($env:SEER_JOB_NAME) { $env:SEER_JOB_NAME } else { "a" }
$heartbeatName = if ($env:SEER_HEARTBEAT_NAME) { $env:SEER_HEARTBEAT_NAME } else { $jobName }

Section "0) Build local seer binary"
Push-Location $CliDir
try {
    go build -o seer.exe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Pop-Location
}
$SeerBin = Join-Path $CliDir "seer.exe"
Write-Host "Using binary: $SeerBin"

$queueDir = Join-Path ([System.IO.Path]::GetTempPath()) ("seer-cli-smoke-queue-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $queueDir | Out-Null
$deadDir = Join-Path $queueDir "dead"
New-Item -ItemType Directory -Path $deadDir | Out-Null
$env:SEER_QUEUE_DIR = $queueDir
$env:SEER_TIMEOUT = "30"

Write-Host "queue_dir=$queueDir"
Write-Host "job_name=$jobName heartbeat_name=$heartbeatName"
Write-Host "base_url=$baseUrl"

$failures = 0
function Expect-Exit([int]$Actual, [int]$Expected, [string]$Label) {
    if ($Actual -ne $Expected) {
        Write-Host "FAIL: $Label (exit $Actual, expected $Expected)"
        $script:failures++
    } else {
        Write-Host "OK: $Label (exit $Actual)"
    }
}

function Run-Seer([string[]]$SeerArgs) {
    # Keep CLI output visible without capturing it into the return value.
    & $SeerBin @SeerArgs 2>&1 | ForEach-Object { Write-Host $_ }
    return [int]$LASTEXITCODE
}

# ---- 1) version ----
Section "1) version"
$out = & $SeerBin version
Write-Host $out
Expect-Exit $LASTEXITCODE 0 "seer version"
if ($out -notmatch "0\.2\.4") {
    Write-Host "FAIL: expected version 0.2.4, got: $out"
    $failures++
} else {
    Write-Host "OK: version string is 0.2.4"
}

# ---- 2) help ----
Section "2) help"
$null = & $SeerBin help
Expect-Exit $LASTEXITCODE 0 "seer help"

# ---- 3) success run ----
Section "3) run success (logs, metadata, tags, auto-replay)"
$code = Run-Seer @(
    "run", $jobName,
    "--base-url=$baseUrl",
    '--metadata={"suite":"cli_live_smoke","case":"success"}',
    "--tags=smoke,success,cli",
    "--",
    "powershell", "-NoProfile", "-Command",
    "Write-Host 'hello from cli smoke success'; Start-Sleep -Milliseconds 400"
)
Expect-Exit $code 0 "success run"

# ---- 4) failure run ----
Section "4) run failure (expect exit 42)"
$code = Run-Seer @(
    "run", $jobName,
    "--base-url=$baseUrl",
    '--metadata={"suite":"cli_live_smoke","case":"failure"}',
    "--tags=smoke,failure,cli",
    "--",
    "powershell", "-NoProfile", "-Command",
    "Write-Host 'about to fail'; exit 42"
)
Expect-Exit $code 42 "failed run preserves exit 42"

# ---- 5) capture-logs=false ----
Section "5) run with --capture-logs=false"
$code = Run-Seer @(
    "run", $jobName,
    "--base-url=$baseUrl",
    "--capture-logs=false",
    '--metadata={"suite":"cli_live_smoke","case":"no_logs"}',
    "--tags=smoke,nologs,cli",
    "--",
    "powershell", "-NoProfile", "-Command",
    "Write-Host 'no log capture'"
)
Expect-Exit $code 0 "capture-logs=false"

# ---- 6) --no-auto-replay ----
Section "6) run with --no-auto-replay"
$code = Run-Seer @(
    "run", $jobName,
    "--base-url=$baseUrl",
    "--no-auto-replay",
    '--metadata={"suite":"cli_live_smoke","case":"no_auto_replay"}',
    "--tags=smoke,cli",
    "--",
    "powershell", "-NoProfile", "-Command",
    "Write-Host 'no auto replay'"
)
Expect-Exit $code 0 "no-auto-replay"

# ---- 7) heartbeat ----
Section "7) heartbeat (metadata + tags)"
$code = Run-Seer @(
    "heartbeat", $heartbeatName,
    "--base-url=$baseUrl",
    "--metadata={`"suite`":`"cli_live_smoke`",`"pid`":$PID}",
    "--tags=smoke,heartbeat,cli"
)
Expect-Exit $code 0 "heartbeat"

# ---- 8) manual replay ----
Section "8) replay"
$code = Run-Seer @("replay", "--base-url=$baseUrl")
Expect-Exit $code 0 "replay"

# ---- 9) replay-failed alias ----
Section "9) replay-failed alias"
$code = Run-Seer @("replay-failed", "--base-url=$baseUrl", "--max-attempts=3")
Expect-Exit $code 0 "replay-failed alias"

# ---- 10) offline unreachable host ----
Section "10) unreachable base_url queues final outcome"
$code = Run-Seer @(
    "run", $jobName,
    "--base-url=https://127.0.0.1:9",
    "--no-auto-replay",
    '--metadata={"suite":"cli_live_smoke","case":"offline"}',
    "--tags=smoke,offline,cli",
    "--",
    "powershell", "-NoProfile", "-Command",
    "Write-Host 'job still runs while Seer is unreachable'"
)
Expect-Exit $code 0 "offline run still succeeds"

$queued = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
Write-Host "Queued envelopes: $($queued.Count)"
if ($queued.Count -lt 1) {
    Write-Host "FAIL: expected at least one queued envelope"
    $failures++
} else {
    foreach ($f in $queued) {
        Write-Host "  - $($f.Name)"
        $envJson = Get-Content $f.FullName -Raw | ConvertFrom-Json
        if ($envJson.base_url -ne "https://127.0.0.1:9") {
            Write-Host "FAIL: envelope base_url pin mismatch: $($envJson.base_url)"
            $failures++
        } else {
            Write-Host "OK: envelope pinned to https://127.0.0.1:9"
        }
        if ($envJson.payload.status -eq "running") {
            Write-Host "FAIL: queued forever-running stub"
            $failures++
        } else {
            Write-Host "OK: final status=$($envJson.payload.status)"
        }
        $rid = [string]$envJson.payload.run_id
        if (-not [string]::IsNullOrEmpty($rid)) {
            Write-Host "FAIL: expected empty run_id for offline final, got '$rid'"
            $failures++
        } else {
            Write-Host "OK: empty run_id for offline final"
        }
    }
}

# ---- 11) pinned replay should not mis-route ----
Section "11) pinned replay against unreachable host"
$code = Run-Seer @("replay", "--base-url=$baseUrl", "--max-attempts=1")
Write-Host "replay exit=$code (non-zero OK when pinned host unreachable)"
$after = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
$afterDead = @(Get-ChildItem -Path $deadDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
Write-Host "Queue remaining=$($after.Count) dead=$($afterDead.Count)"
if (($after.Count + $afterDead.Count) -lt 1) {
    Write-Host "FAIL: pinned offline envelope disappeared without dead-letter or retry file"
    $failures++
} else {
    Write-Host "OK: pinned envelope retained or dead-lettered"
}

Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue | Remove-Item -Force
Get-ChildItem -Path $deadDir -Filter "*.json" -File -ErrorAction SilentlyContinue | Remove-Item -Force

# ---- 12) offline envelope + real replay ----
Section "12) craft offline envelope + replay register/complete"
$stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddHHmmssffffff")
$idem = [guid]::NewGuid().ToString()
$envelopeObj = [ordered]@{
    version = 3
    endpoint = "monitoring"
    base_url = $baseUrl.TrimEnd("/")
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    attempts = 0
    idempotency_key = $idem
    payload = [ordered]@{
        job_name = $jobName
        status = "success"
        run_id = ""
        start_time = (Get-Date).ToUniversalTime().ToString("o")
        end_time = (Get-Date).ToUniversalTime().ToString("o")
        metadata = [ordered]@{ suite = "cli_live_smoke"; case = "offline_replay" }
        error_details = $null
        tags = @("smoke", "offline", "cli")
        logs = "queued offline then replayed by cli smoke`n"
    }
}
$envPath = Join-Path $queueDir "${stamp}_monitoring_smoketest.json"
($envelopeObj | ConvertTo-Json -Depth 6) | Set-Content -Path $envPath -Encoding utf8
Write-Host "Queued $envPath (Idempotency-Key=$idem)"

$code = Run-Seer @("replay", "--base-url=$baseUrl")
Expect-Exit $code 0 "offline register+complete replay"
$remaining = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
if ($remaining.Count -ne 0) {
    Write-Host "FAIL: queue not empty after successful offline replay: $($remaining.Name -join ', ')"
    $failures++
} else {
    Write-Host "OK: queue empty after offline replay"
}

# ---- 13) background-replay ----
Section "13) background-replay during run"
$hbStamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddHHmmssffffff")
$hbObj = [ordered]@{
    version = 3
    endpoint = "heartbeat"
    base_url = $baseUrl.TrimEnd("/")
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    attempts = 0
    idempotency_key = [guid]::NewGuid().ToString()
    payload = [ordered]@{
        job_name = $heartbeatName
        current_time = (Get-Date).ToUniversalTime().ToString("o")
        metadata = [ordered]@{ suite = "cli_live_smoke"; case = "background" }
        tags = @("smoke", "background", "cli")
    }
}
($hbObj | ConvertTo-Json -Depth 6) | Set-Content -Path (Join-Path $queueDir "${hbStamp}_heartbeat_smoketest.json") -Encoding utf8

$code = Run-Seer @(
    "run", $jobName,
    "--base-url=$baseUrl",
    "--no-auto-replay",
    "--background-replay",
    "--replay-interval=2",
    '--metadata={"suite":"cli_live_smoke","case":"background_replay"}',
    "--tags=smoke,background,cli",
    "--",
    "powershell", "-NoProfile", "-Command",
    "Write-Host 'waiting for background flusher'; Start-Sleep -Seconds 5"
)
Expect-Exit $code 0 "background-replay run"

$remaining = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
Write-Host "Queue remaining after background run: $($remaining.Count)"
if ($remaining.Count -ne 0) {
    Write-Host "WARN: queue not fully flushed; trying manual replay"
    $null = Run-Seer @("replay", "--base-url=$baseUrl")
    $remaining = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
}
if ($remaining.Count -ne 0) {
    Write-Host "FAIL: queue still has files: $($remaining.Name -join ', ')"
    $failures++
} else {
    Write-Host "OK: background/manual replay cleared queue"
}

Section "DONE"
Write-Host "Live smoke finished with $failures assertion failure(s)."
Write-Host "Temp queue left at: $queueDir"
Write-Host "If you saw Job/Pipeline Does Not Exist, create the job in the dashboard and set:"
Write-Host '  $env:SEER_JOB_NAME = ''your_exact_job_name'''
if ($failures -gt 0) { exit 1 }
exit 0
