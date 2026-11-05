# Builds the web UI and netchat binaries into bin/.
#   ./build.ps1          UI + Windows binary
#   ./build.ps1 -All     UI + Windows, Linux, macOS (macOS without tray: needs cgo, build on a Mac for the tray)
param([switch]$All, [switch]$SkipUI)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

if (-not $SkipUI) {
  Push-Location web
  if (-not (Test-Path node_modules)) { npm ci }
  npm run build
  Pop-Location
}

$ldflags = '-s -w'
$env:CGO_ENABLED = '0'
$targets = @(@{ os = 'windows'; arch = 'amd64'; out = 'bin/netchat.exe' })
if ($All) {
  $targets += @{ os = 'windows'; arch = 'arm64'; out = 'bin/netchat-windows-arm64.exe' }
  $targets += @{ os = 'linux'; arch = 'amd64'; out = 'bin/netchat-linux-amd64' }
  $targets += @{ os = 'linux'; arch = 'arm64'; out = 'bin/netchat-linux-arm64' }
  $targets += @{ os = 'darwin'; arch = 'arm64'; out = 'bin/netchat-macos-arm64' }
  $targets += @{ os = 'darwin'; arch = 'amd64'; out = 'bin/netchat-macos-amd64' }
}
foreach ($t in $targets) {
  $env:GOOS = $t.os; $env:GOARCH = $t.arch
  go build -trimpath -ldflags $ldflags -o $t.out ./cmd/netchat
  if ($LASTEXITCODE) { throw "build failed: $($t.os)/$($t.arch)" }
  Write-Host "built $($t.out)"
}
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
