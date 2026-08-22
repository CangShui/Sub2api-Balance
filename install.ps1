$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Source = Join-Path $Root 'dist\sub2api.exe'
$TargetDir = Join-Path $env:USERPROFILE 'bin'
$Target = Join-Path $TargetDir 'sub2api.exe'

if (-not (Test-Path $Source)) {
    throw "找不到 $Source，请先运行 .\build.ps1"
}

New-Item -ItemType Directory -Force -Path $TargetDir | Out-Null
Copy-Item $Source $Target -Force

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathItems = @()
if ($userPath) {
    $pathItems = $userPath -split ';' | Where-Object { $_ -ne '' }
}
if ($pathItems -notcontains $TargetDir) {
    [Environment]::SetEnvironmentVariable('Path', (($pathItems + $TargetDir) -join ';'), 'User')
}
$env:Path = "$TargetDir;$env:Path"

Write-Host "已安装：$Target"
Write-Host '当前 PowerShell 会话已可直接使用 sub2api；新开终端后同样生效。'
Write-Host '用法：sub2api 或 sub2api -f [秒数]'
