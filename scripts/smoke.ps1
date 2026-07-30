$ErrorActionPreference = "Stop"

$listeners = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
    Where-Object { $_.LocalPort -in 8080, 9090 }
if ($listeners) {
    throw "Ports 8080 or 9090 are already in use."
}

$env:RELAY_CHANNEL_DEMO_SECRET = "local-smoke-secret-at-least-32-characters"
$env:RELAY_ALLOW_PRIVATE_TARGETS = "true"
$env:DEMO_FAIL_FIRST = "1"
$receiverProcess = $null
$relayProcess = $null

try {
    $receiverProcess = Start-Process `
        -FilePath "$PSScriptRoot\..\bin\demo-receiver.exe" `
        -RedirectStandardOutput "$PSScriptRoot\..\receiver-smoke.log" `
        -RedirectStandardError "$PSScriptRoot\..\receiver-smoke.err.log" `
        -PassThru `
        -WindowStyle Hidden
    $relayProcess = Start-Process `
        -FilePath "$PSScriptRoot\..\bin\relay.exe" `
        -ArgumentList "-config", "config/config.local.example.json" `
        -RedirectStandardOutput "$PSScriptRoot\..\relay-smoke.log" `
        -RedirectStandardError "$PSScriptRoot\..\relay-smoke.err.log" `
        -PassThru `
        -WindowStyle Hidden

    $ready = $false
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        try {
            $health = Invoke-RestMethod -Uri "http://127.0.0.1:8080/health" -TimeoutSec 1
            if ($health.status -eq "ok") {
                $ready = $true
                break
            }
        } catch {
            # Startup connection failures are expected during this bounded wait.
        }
        Start-Sleep -Milliseconds 250
    }
    if (-not $ready) {
        throw "Relay did not become healthy."
    }

    $payload = '{"event":"smoke.test","id":42}'
    $payloadBytes = [System.Text.Encoding]::UTF8.GetBytes($payload)
    $secretBytes = [System.Text.Encoding]::UTF8.GetBytes($env:RELAY_CHANNEL_DEMO_SECRET)
    $hmac = New-Object System.Security.Cryptography.HMACSHA256
    try {
        $hmac.Key = $secretBytes
        $digest = [BitConverter]::ToString($hmac.ComputeHash($payloadBytes)).Replace("-", "").ToLowerInvariant()
    } finally {
        $hmac.Dispose()
    }

    $accepted = Invoke-RestMethod `
        -Method Post `
        -Uri "http://127.0.0.1:8080/webhooks/demo" `
        -Headers @{
            "X-Webhook-Signature" = "sha256=$digest"
            "Idempotency-Key" = "local-smoke-42"
        } `
        -ContentType "application/json" `
        -Body $payload

    $delivered = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        $delivery = Invoke-RestMethod `
            -Uri "http://127.0.0.1:8080/deliveries/$($accepted.delivery.id)"
        if ($delivery.status -eq "delivered" -and $delivery.attempts -eq 2) {
            $delivered = $true
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if (-not $delivered) {
        throw "Delivery did not complete after the expected retry."
    }

    $metrics = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8080/metrics"
    [pscustomobject]@{
        Health = $health.status
        DeliveryStatus = $delivery.status
        Attempts = $delivery.attempts
        MetricsStatus = $metrics.StatusCode
    }
} finally {
    if ($relayProcess -and -not $relayProcess.HasExited) {
        Stop-Process -Id $relayProcess.Id -Force
    }
    if ($receiverProcess -and -not $receiverProcess.HasExited) {
        Stop-Process -Id $receiverProcess.Id -Force
    }
}
