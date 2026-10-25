$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    Push-Location web
    try {
        & npm.cmd ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        & npm.cmd run build
        if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' }
    } finally { Pop-Location }
    $static = Join-Path $PSScriptRoot 'cmd\reader\static'
    New-Item -ItemType Directory -Force $static | Out-Null
    Copy-Item 'web\dist\*' $static -Recurse -Force
    New-Item -ItemType Directory -Force 'dist' | Out-Null
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & go build -trimpath -o dist\reader.exe ./cmd/reader
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    Write-Output 'Built dist\reader.exe with embedded frontend'
} finally { Pop-Location }
