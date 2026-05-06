param(
    [string]$SecretFile = "release_secret.txt",
    [switch]$SkipTests,
    [switch]$BuildInstaller
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$authPath = Join-Path $repoRoot "auth.go"
$exePath = Join-Path $repoRoot "calendarr.exe"
$checksumPath = Join-Path $repoRoot "calendarr.exe.sha256"
$secretPath = Join-Path $repoRoot $SecretFile
$installerScriptPath = Join-Path $repoRoot "calendarr.iss"
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

        if ($BuildInstaller) {
            $iscc = Get-Command "ISCC.exe" -ErrorAction SilentlyContinue
            $isccPath = if ($iscc) { $iscc.Source } else { "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" }
            if (-not (Test-Path -LiteralPath $isccPath)) {
                throw "ISCC.exe was not found. Install Inno Setup 6 or add ISCC.exe to PATH."
            }
            & $isccPath $installerScriptPath
        }
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

if ($BuildInstaller) {
    Write-Host "Built calendarr.exe, calendarr.exe.sha256, and the installer."
} else {
    Write-Host "Built calendarr.exe and calendarr.exe.sha256."
}
