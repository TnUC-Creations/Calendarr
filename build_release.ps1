param(
    [string]$SecretFile = "release_secret.txt",
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$authPath = Join-Path $repoRoot "auth.go"
$exePath = Join-Path $repoRoot "calendarr.exe"
$checksumPath = Join-Path $repoRoot "calendarr.exe.sha256"
$secretPath = Join-Path $repoRoot $SecretFile
$placeholder = "REPLACE_WITH_RELEASE_GOOGLE_CLIENT_SECRET"

if (-not (Test-Path -LiteralPath $secretPath)) {
    throw "Missing $SecretFile. Create it locally with the Google OAuth client secret. This file is ignored and must not be committed."
}

$secret = (Get-Content -Raw -LiteralPath $secretPath).Trim()
if ([string]::IsNullOrWhiteSpace($secret)) {
    throw "$SecretFile is empty."
}

$originalAuth = Get-Content -Raw -LiteralPath $authPath
if ($originalAuth -notlike "*$placeholder*") {
    throw "auth.go does not contain the release secret placeholder. Refusing to build."
}

try {
    $patchedAuth = $originalAuth.Replace($placeholder, $secret)
    Set-Content -LiteralPath $authPath -Value $patchedAuth -NoNewline

    Push-Location $repoRoot
    try {
        $env:GOCACHE = Join-Path $repoRoot ".gocache"
        if (-not $SkipTests) {
            go test ./...
        }
        go build -trimpath -ldflags="-H windowsgui" -o $exePath .
        Get-FileHash -Algorithm SHA256 $exePath |
            ForEach-Object { "$($_.Hash.ToLower())  calendarr.exe" } |
            Set-Content -LiteralPath $checksumPath -NoNewline
    }
    finally {
        Pop-Location
    }
}
finally {
    Set-Content -LiteralPath $authPath -Value $originalAuth -NoNewline
}

$postBuildAuth = Get-Content -Raw -LiteralPath $authPath
if ($postBuildAuth -notlike "*$placeholder*") {
    throw "auth.go was not restored to the placeholder."
}

Write-Host "Built calendarr.exe and calendarr.exe.sha256."
