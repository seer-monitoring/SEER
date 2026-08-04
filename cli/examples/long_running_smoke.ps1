# Long-running CLI job smoke against local CE server.
$ErrorActionPreference = "Stop"

$SeerBin = "E:\portfolio\SEER\cli\seer.exe"
$baseUrl = if ($env:SEER_BASE_URL) { $env:SEER_BASE_URL } else { "http://127.0.0.1:18080" }
$apiKey = if ($env:SEER_API_KEY) { $env:SEER_API_KEY } else { "dev-key" }
$job = "cli_long_running"
$queueDir = Join-Path $env:TEMP ("seer-cli-longrun-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $queueDir | Out-Null

$env:SEER_API_KEY = $apiKey
$env:SEER_QUEUE_DIR = $queueDir
$env:SEER_TIMEOUT = "30"

Write-Host "queue=$queueDir"
Write-Host "base=$baseUrl job=$job"

# Seed queued heartbeat for background-replay during the long run
$stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddHHmmssffffff")
$seed = [ordered]@{
    version         = 3
    endpoint        = "heartbeat"
    base_url        = $baseUrl.TrimEnd("/")
    created_at      = (Get-Date).ToUniversalTime().ToString("o")
    attempts        = 0
    idempotency_key = [guid]::NewGuid().ToString()
    payload         = [ordered]@{
        job_name     = $job
        current_time = (Get-Date).ToUniversalTime().ToString("o")
        metadata     = [ordered]@{ suite = "cli_long_running"; case = "seed" }
        tags         = @("longrun", "seed", "cli")
    }
}
($seed | ConvertTo-Json -Depth 6) | Set-Content (Join-Path $queueDir "${stamp}_heartbeat_seed.json") -Encoding utf8
Write-Host "Seeded queued heartbeat"

$failures = 0
$childScript = @'
Write-Host "long job started"
for ($i = 1; $i -le 8; $i++) {
    Write-Host "tick $i/8"
    Start-Sleep -Seconds 5
}
Write-Host "long job done"
'@
$childPath = Join-Path $queueDir "child_job.ps1"
Set-Content -Path $childPath -Value $childScript -Encoding utf8

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $SeerBin
$psi.ArgumentList.Add("run")
$psi.ArgumentList.Add($job)
$psi.ArgumentList.Add("--base-url=$baseUrl")
$psi.ArgumentList.Add("--no-auto-replay")
$psi.ArgumentList.Add("--background-replay")
$psi.ArgumentList.Add("--replay-interval=5")
$psi.ArgumentList.Add('--metadata={"suite":"cli_long_running","case":"main"}')
$psi.ArgumentList.Add("--tags=longrun,cli")
$psi.ArgumentList.Add("--")
$psi.ArgumentList.Add("powershell")
$psi.ArgumentList.Add("-NoProfile")
$psi.ArgumentList.Add("-File")
$psi.ArgumentList.Add($childPath)
$psi.UseShellExecute = $false
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true
$psi.Environment["SEER_API_KEY"] = $apiKey
$psi.Environment["SEER_QUEUE_DIR"] = $queueDir
$psi.Environment["SEER_TIMEOUT"] = "30"

$proc = New-Object System.Diagnostics.Process
$proc.StartInfo = $psi
$null = $proc.Start()
Write-Host "Long run pid=$($proc.Id) (~40s with mid-run heartbeats)"

Start-Sleep -Seconds 4
for ($i = 1; $i -le 5; $i++) {
    Write-Host "--- heartbeat $i ---"
    $meta = "{`"suite`":`"cli_long_running`",`"hb`":$i}"
    & $SeerBin heartbeat $job "--base-url=$baseUrl" "--metadata=$meta" "--tags=longrun,heartbeat,cli"
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL: heartbeat $i exit=$LASTEXITCODE"
        $failures++
    } else {
        Write-Host "OK: heartbeat $i"
    }
    Start-Sleep -Seconds 6
}

$stdoutTask = $proc.StandardOutput.ReadToEndAsync()
$stderrTask = $proc.StandardError.ReadToEndAsync()
$proc.WaitForExit()
$stdout = $stdoutTask.Result
$stderr = $stderrTask.Result

Write-Host "---- long run stdout ----"
Write-Host $stdout
if ($stderr) {
    Write-Host "---- stderr ----"
    Write-Host $stderr
}
Write-Host "Long run exit=$($proc.ExitCode)"
if ($proc.ExitCode -ne 0) {
    Write-Host "FAIL: long run exit $($proc.ExitCode)"
    $failures++
} else {
    Write-Host "OK: long run completed"
}

$remaining = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
Write-Host "Queue remaining: $($remaining.Count)"
if ($remaining.Count -gt 0) {
    Write-Host "Manual replay fallback..."
    & $SeerBin replay "--base-url=$baseUrl"
    $remaining = @(Get-ChildItem -Path $queueDir -Filter "*.json" -File -ErrorAction SilentlyContinue)
}
if ($remaining.Count -ne 0) {
    Write-Host "FAIL: queue not empty: $($remaining.Name -join ', ')"
    $failures++
} else {
    Write-Host "OK: queue flushed"
}

try {
    $hb = Invoke-RestMethod -Uri "$baseUrl/check_heartbeat" -Headers @{ Authorization = $apiKey } -Method Get
    Write-Host ("check_heartbeat=" + ($hb | ConvertTo-Json -Compress))
} catch {
    Write-Host "WARN: check_heartbeat failed: $_"
    $failures++
}

Write-Host "LONGRUN_FAILURES=$failures"
if ($failures -gt 0) { exit 1 }
exit 0
