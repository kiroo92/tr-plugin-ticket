#Requires -Version 7.4
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$names = @('GOOS', 'GOARCH', 'CGO_ENABLED', 'TMP', 'TEMP', 'GOMAXPROCS', 'GOFLAGS', 'GOMEMLIMIT')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
Push-Location $PSScriptRoot
try {
    $env:GOMAXPROCS = '2'
    $env:GOFLAGS = '-p=1'
    $env:GOMEMLIMIT = '1GiB'
    $env:GOOS = $null
    $env:GOARCH = $null
    go test ./...
    go vet ./...
    pnpm --dir web install --frozen-lockfile
    pnpm --dir web test
    pnpm --dir web build
    $env:CGO_ENABLED = '0'
    foreach ($platform in @('linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64')) {
        $env:GOOS, $env:GOARCH = $platform.Split('/')
        $binaryDirectory = Join-Path $PSScriptRoot "dist/$platform/bin"
        New-Item -ItemType Directory -Path $binaryDirectory -Force | Out-Null
        $binaryName = if ($env:GOOS -eq 'windows') { 'plugin.exe' } else { 'plugin' }
        Write-Host "Building $platform"
        go build -trimpath -ldflags='-s -w' -o (Join-Path $binaryDirectory $binaryName) ./cmd/plugin
    }
    $env:GOOS = $null
    $env:GOARCH = $null
    $packageTemp = Join-Path $PSScriptRoot 'dist/tmp'
    New-Item -ItemType Directory -Path $packageTemp -Force | Out-Null
    $env:TMP = $packageTemp
    $env:TEMP = $packageTemp
    go tool trplugin pack --manifest plugin.json --frontend dist/frontend --bin-dir dist --out release
    $manifest = Get-Content -LiteralPath plugin.json -Raw | ConvertFrom-Json
    $packages = @(Get-ChildItem -LiteralPath release -Filter "$($manifest.id)-$($manifest.version)-*.tar.gz" | ForEach-Object FullName)
    if ($packages.Count -ne 5) { throw 'Expected five platform packages' }
    go tool trplugin validate @packages
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
    Pop-Location
}
