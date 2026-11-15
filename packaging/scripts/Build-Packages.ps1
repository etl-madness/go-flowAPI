<#
.SYNOPSIS
    Cross-compiles Unix binaries (go-flowAPI), stages compressed man/info documentation,
    builds Windows .zip/.msi + WinGet manifests, and builds .deb/.rpm/Arch packages via nfpm.

.DESCRIPTION
    Useful on Windows workstations or CI runners to verify cross-compilation for
    Linux, macOS, and FreeBSD, compress man/info documentation, build Windows packages,
    and optionally produce .deb, .rpm, and Arch .pkg.tar.zst packages via nfpm without WSL.

.EXAMPLE
    .\packaging\scripts\Build-Packages.ps1 -Target stage-all
    .\packaging\scripts\Build-Packages.ps1 -Target windows -Arch amd64
#>
[CmdletBinding()]
param(
    [ValidateSet("stage-all", "linux", "darwin", "freebsd", "windows", "winget", "msi", "zip", "deb", "rpm", "arch", "archlinux")]
    [string]$Target = "stage-all",

    [ValidateSet("amd64", "arm64")]
    [string]$Arch = "amd64",

    [string]$Version = "",

    [string]$Release = "1"
)

$ErrorActionPreference = "Stop"

$ScriptsDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$PackagingDir = Split-Path -Parent $ScriptsDir
$RepoRoot = Split-Path -Parent $PackagingDir
$BuildRoot = Join-Path $PackagingDir "build"
$DistDir = Join-Path $PackagingDir "dist"

if ([string]::IsNullOrWhiteSpace($Version)) {
    try {
        $rawTag = git -C $RepoRoot describe --tags --abbrev=0 2>$null
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($rawTag)) {
            $Version = $rawTag -replace '^[vV]', ''
        } else {
            $Version = "1.2.1"
        }
    } catch {
        $Version = "1.2.1"
    }
}

function Compress-GzipFile {
    param([string]$SourcePath, [string]$DestPath)
    $destParent = Split-Path -Parent $DestPath
    if (-not (Test-Path $destParent)) {
        New-Item -ItemType Directory -Path $destParent -Force | Out-Null
    }
    $inBytes = [System.IO.File]::ReadAllBytes($SourcePath)
    $outStream = [System.IO.File]::Create($DestPath)
    try {
        $gzipStream = New-Object System.IO.Compression.GZipStream($outStream, [System.IO.Compression.CompressionLevel]::Optimal)
        try {
            $gzipStream.Write($inBytes, 0, $inBytes.Length)
        } finally {
            $gzipStream.Dispose()
        }
    } finally {
        $outStream.Dispose()
    }
}

function Stage-Docs {
    param([string]$DocDir)
    Compress-GzipFile (Join-Path $PackagingDir "man\man1\go-flowAPI.1") (Join-Path $DocDir "man1\go-flowAPI.1.gz")
    Compress-GzipFile (Join-Path $PackagingDir "man\man5\go-flow-api.env.5") (Join-Path $DocDir "man5\go-flow-api.env.5.gz")
    Compress-GzipFile (Join-Path $PackagingDir "info\go-flow-api.info") (Join-Path $DocDir "info\go-flow-api.info.gz")
    Copy-Item -Force (Join-Path $PackagingDir "info\dir") (Join-Path $DocDir "info\dir")
}

function Build-GoBinary {
    param([string]$GoOS, [string]$GoArch, [string]$OutDir)
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
    Write-Host "[INFO] Cross-compiling go-flowAPI for $GoOS/$GoArch..."

    Push-Location $RepoRoot
    try {
        $env:CGO_ENABLED = "0"
        $env:GOOS = $GoOS
        $env:GOARCH = $GoArch

        go build -trimpath -ldflags="-s -w" -o (Join-Path $OutDir "go-flowAPI") .
        if ($LASTEXITCODE -ne 0) { throw "go build failed for go-flowAPI ($GoOS/$GoArch)" }
    } finally {
        Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
        Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
        Pop-Location
    }
}

$DocStage = Join-Path $BuildRoot "docs"
Stage-Docs -DocDir $DocStage
New-Item -ItemType Directory -Path $DistDir -Force | Out-Null

switch ($Target) {
    "stage-all" {
        Build-GoBinary -GoOS "linux" -GoArch $Arch -OutDir (Join-Path $BuildRoot "stage\linux-$Arch\bin")
        Build-GoBinary -GoOS "darwin" -GoArch $Arch -OutDir (Join-Path $BuildRoot "stage\darwin-$Arch\bin")
        Build-GoBinary -GoOS "freebsd" -GoArch $Arch -OutDir (Join-Path $BuildRoot "stage\freebsd-$Arch\bin")
        Write-Host "[INFO] Staged binaries and compressed man/info docs in $BuildRoot"
    }
    "linux" {
        Build-GoBinary -GoOS "linux" -GoArch $Arch -OutDir (Join-Path $BuildRoot "stage\linux-$Arch\bin")
    }
    "darwin" {
        Build-GoBinary -GoOS "darwin" -GoArch $Arch -OutDir (Join-Path $BuildRoot "stage\darwin-$Arch\bin")
    }
    "freebsd" {
        Build-GoBinary -GoOS "freebsd" -GoArch $Arch -OutDir (Join-Path $BuildRoot "stage\freebsd-$Arch\bin")
    }
    { $_ -in @("windows", "winget", "msi", "zip") } {
        $winFmt = if ($_ -in @("msi", "zip")) { $_ } else { "all" }
        & (Join-Path $ScriptsDir "Build-Windows.ps1") -Format $winFmt -Arch $Arch -Version $Version
    }
    { $_ -in @("deb", "rpm", "arch", "archlinux") } {
        $packager = if ($_ -in @("arch", "archlinux")) { "archlinux" } else { $_ }
        if (-not (Get-Command nfpm -ErrorAction SilentlyContinue)) {
            $scriptHint = if ($packager -eq "archlinux") { "build-arch.sh" } else { "build-$packager.sh" }
            throw "nfpm is required on Windows to build $Target directly (or run packaging/scripts/$scriptHint on Linux/WSL)."
        }
        $binDir = Join-Path $BuildRoot "stage\linux-$Arch\bin"
        Build-GoBinary -GoOS "linux" -GoArch $Arch -OutDir $binDir

        $env:REPO_ROOT = $RepoRoot
        $env:STAGE_BIN_DIR = $binDir
        $env:STAGE_DOC_DIR = $DocStage
        $env:NFPM_ARCH = $Arch
        $env:FLOW_RELEASE = $Release

        if ($packager -eq "deb") {
            $env:FLOW_VERSION = $Version
            $outFile = Join-Path $DistDir "go-flow-api_${Version}-${Release}_${Arch}.deb"
        } elseif ($packager -eq "rpm") {
            $env:FLOW_VERSION = ($Version -replace '-', '~')
            $rpmArch = if ($Arch -eq "amd64") { "x86_64" } else { "aarch64" }
            $outFile = Join-Path $DistDir "go-flow-api-$($env:FLOW_VERSION)-${Release}.${rpmArch}.rpm"
        } else {
            $env:FLOW_VERSION = ($Version -replace '-', '_')
            $archCpu = if ($Arch -eq "amd64") { "x86_64" } else { "aarch64" }
            $outFile = Join-Path $DistDir "go-flow-api-$($env:FLOW_VERSION)-${Release}-${archCpu}.pkg.tar.zst"
        }

        nfpm package --config (Join-Path $PackagingDir "nfpm.yaml") --packager $packager --target $outFile
        if ($LASTEXITCODE -ne 0) { throw "nfpm failed for $packager" }
        Write-Host "[INFO] Created package: $outFile"
    }
}
