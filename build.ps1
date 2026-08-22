$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Dist = Join-Path $Root 'dist'
$Version = '0.1.0'

New-Item -ItemType Directory -Force -Path $Dist | Out-Null

Push-Location $Root
try {
    go test ./...
    $ldflags = "-s -w -X main.version=$Version"

    $oldGOOS = $env:GOOS
    $oldGOARCH = $env:GOARCH
    $env:GOARCH = 'amd64'

    $env:GOOS = 'windows'
    go build -trimpath -ldflags $ldflags -o (Join-Path $Dist 'sub2api-windows-amd64.exe') .

    $env:GOOS = 'linux'
    go build -trimpath -ldflags $ldflags -o (Join-Path $Dist 'sub2api-linux-amd64') .

    Copy-Item (Join-Path $Dist 'sub2api-windows-amd64.exe') (Join-Path $Dist 'sub2api.exe') -Force
    Write-Host "Built: $Dist\sub2api.exe"
    Write-Host "Built: $Dist\sub2api-windows-amd64.exe"
    Write-Host "Built: $Dist\sub2api-linux-amd64"
}
finally {
    if ($null -eq $oldGOOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGOOS }
    if ($null -eq $oldGOARCH) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGOARCH }
    Pop-Location
}
