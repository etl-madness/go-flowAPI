<#
.SYNOPSIS
    Builds Windows WinGet-compatible packages (.zip portable archive and .msi installer)
    and generates validated WinGet 1.6.0 multi-file YAML manifests for ETLMadness.GoFlowAPI.

.DESCRIPTION
    Compiles go-flowAPI.exe (with native Windows SCM service support), bundles SQL initialization
    DDL scripts, reference guides, and PowerShell Windows Service setup scripts, builds:
      1. go-flow-api-<version>-windows-<arch>.zip (WinGet NestedInstallerType: portable)
      2. go-flow-api-<version>-windows-<arch>.msi (WiX v4/v5/v6 'wix', WiX v3 'candle/light', or 'wixl')
    and generates WinGet manifests in packaging/dist/winget/e/ETLMadness/GoFlowAPI/<version>/.

.EXAMPLE
    .\packaging\scripts\Build-Windows.ps1
    .\packaging\scripts\Build-Windows.ps1 -Format all -Arch both
    .\packaging\scripts\Build-Windows.ps1 -Format msi -Arch amd64 -RequireMsi
#>
[CmdletBinding()]
param(
    [ValidateSet("all", "zip", "msi")]
    [string]$Format = "all",

    [ValidateSet("amd64", "arm64", "both")]
    [string]$Arch = "amd64",

    [string]$Version = "",

    [string]$ReleaseBaseUrl = "",

    [string]$SignCertThumbprint = "",

    [string]$TimestampServer = "http://timestamp.digicert.com",

    [switch]$RequireMsi,

    [switch]$SkipWingetValidate
)

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

$ScriptsDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$PackagingDir = Split-Path -Parent $ScriptsDir
$RepoRoot = Split-Path -Parent $PackagingDir
$BuildRoot = Join-Path $PackagingDir "build"
$DistDir = Join-Path $PackagingDir "dist"
$UpgradeCode = "{4B9E2C71-8A54-4D39-B61E-9C20F5A4D831}"

if ([string]::IsNullOrWhiteSpace($Version)) {
    try {
        $rawTag = git -C $RepoRoot describe --tags --abbrev=0 2>$null
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($rawTag)) {
            $Version = $rawTag -replace '^[vV]', ''
        } else {
            $Version = "1.3.2"
        }
    } catch {
        $Version = "1.3.2"
    }
}

if ([string]::IsNullOrWhiteSpace($ReleaseBaseUrl)) {
    $ReleaseBaseUrl = "https://github.com/etl-madness/go-flowAPI/releases/download/v$Version"
}
$ReleaseBaseUrl = $ReleaseBaseUrl.TrimEnd('/')

function Get-MsiNumericVersion {
    param([string]$Ver)
    $clean = ($Ver -split '[-+]')[0]
    $parts = $clean -split '\.'
    $nums = @(0, 0, 0)
    for ($i = 0; $i -lt [Math]::Min(3, $parts.Count); $i++) {
        $val = 0
        if ([int]::TryParse($parts[$i], [ref]$val)) {
            $nums[$i] = [Math]::Min(65535, [Math]::Max(0, $val))
        }
    }
    return "$($nums[0]).$($nums[1]).$($nums[2])"
}

function Get-DeterministicProductCode {
    param([string]$Ver, [string]$GoArch)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes("ETLMadness.GoFlowAPI.MSI:$Ver`:$GoArch")
        $hash = $sha.ComputeHash($bytes)
        $guidBytes = New-Object byte[] 16
        [Array]::Copy($hash, 0, $guidBytes, 0, 16)
        # RFC 4122 version 5 variant bits
        $guidBytes[7] = ($guidBytes[7] -band 0x0F) -bor 0x50
        $guidBytes[8] = ($guidBytes[8] -band 0x3F) -bor 0x80
        $g = New-Object System.Guid (,$guidBytes)
        return "{$($g.ToString().ToUpperInvariant())}"
    } finally {
        $sha.Dispose()
    }
}

function Invoke-AuthenticodeSign {
    param([string]$FilePath)
    if ([string]::IsNullOrWhiteSpace($SignCertThumbprint)) {
        return
    }
    $signtool = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if (-not $signtool) {
        throw "SignCertThumbprint was provided, but signtool.exe was not found in PATH."
    }
    Write-Host "[INFO] Signing $FilePath..."
    & $signtool.Source sign /sha1 $SignCertThumbprint /fd SHA256 /tr $TimestampServer /td SHA256 $FilePath
    if ($LASTEXITCODE -ne 0) {
        throw "signtool.exe failed on $FilePath"
    }
}

function Stage-WindowsPayload {
    param([string]$GoArch, [string]$StageDir)
    if (Test-Path $StageDir) {
        Remove-Item -Recurse -Force $StageDir
    }
    $binDir = Join-Path $StageDir "bin"
    $docsDir = Join-Path $StageDir "docs"
    $sqlDir = Join-Path $StageDir "share\sql"
    $winSvcDir = Join-Path $StageDir "share\services\windows"
    $man1Dir = Join-Path $StageDir "share\man\man1"
    $man5Dir = Join-Path $StageDir "share\man\man5"

    New-Item -ItemType Directory -Path $binDir, $docsDir, $sqlDir, $winSvcDir, $man1Dir, $man5Dir -Force | Out-Null

    Write-Host "[INFO] Compiling go-flowAPI.exe for windows/$GoArch..."
    Push-Location $RepoRoot
    try {
        $env:CGO_ENABLED = "0"
        $env:GOOS = "windows"
        $env:GOARCH = $GoArch

        go build -trimpath -ldflags="-s -w" -o (Join-Path $binDir "go-flowAPI.exe") .
        if ($LASTEXITCODE -ne 0) { throw "go build failed for go-flowAPI.exe (windows/$GoArch)" }
    } finally {
        Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
        Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
        Pop-Location
    }

    Invoke-AuthenticodeSign (Join-Path $binDir "go-flowAPI.exe")

    Copy-Item -Force (Join-Path $RepoRoot "SCHEMA.md") (Join-Path $docsDir "SCHEMA.md")
    Copy-Item -Force (Join-Path $RepoRoot "BOOTSTRAP.md") (Join-Path $docsDir "BOOTSTRAP.md")
    Copy-Item -Force (Join-Path $RepoRoot "docs\API_ENDPOINTS_REFERENCE.md") (Join-Path $docsDir "API_ENDPOINTS_REFERENCE.md")
    Copy-Item -Force (Join-Path $RepoRoot "docs\AD_KERBEROS_SIDECAR_GUIDE.md") (Join-Path $docsDir "AD_KERBEROS_SIDECAR_GUIDE.md")

    Copy-Item -Force (Join-Path $RepoRoot "docker\init\sqlite-init.sql") (Join-Path $sqlDir "sqlite-init.sql")
    Copy-Item -Force (Join-Path $RepoRoot "docker\init\postgres-init.sql") (Join-Path $sqlDir "postgres-init.sql")
    Copy-Item -Force (Join-Path $RepoRoot "docker\init\sqlserver-init.sql") (Join-Path $sqlDir "sqlserver-init.sql")

    Copy-Item -Force (Join-Path $PackagingDir "services\windows\Install-GoFlowAPIService.ps1") (Join-Path $winSvcDir "Install-GoFlowAPIService.ps1")
    Copy-Item -Force (Join-Path $PackagingDir "services\windows\Uninstall-GoFlowAPIService.ps1") (Join-Path $winSvcDir "Uninstall-GoFlowAPIService.ps1")

    $envTemplate = [System.IO.File]::ReadAllText((Join-Path $PackagingDir "etc\go-flow-api\go-flow-api.env"))
    $winEnvTemplate = $envTemplate -replace 'sqlite:///var/lib/go-flow-api/flow_api\.db', 'sqlite://C:/ProgramData/ETL Madness/Flow API/data/flow_api.db'
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText((Join-Path $winSvcDir "go-flow-api.env.example"), $winEnvTemplate, $utf8NoBom)

    Copy-Item -Force (Join-Path $PackagingDir "man\man1\go-flowAPI.1") (Join-Path $man1Dir "go-flowAPI.1")
    Copy-Item -Force (Join-Path $PackagingDir "man\man5\go-flow-api.env.5") (Join-Path $man5Dir "go-flow-api.env.5")
    Copy-Item -Force (Join-Path $RepoRoot "LICENSE") (Join-Path $StageDir "LICENSE.txt")
}

function Build-PortableZip {
    param([string]$StageDir, [string]$OutZip)
    if (Test-Path $OutZip) {
        Remove-Item -Force $OutZip
    }
    Write-Host "[INFO] Creating portable archive: $OutZip"
    $zipStream = [System.IO.File]::Open($OutZip, [System.IO.FileMode]::Create)
    try {
        $archive = New-Object System.IO.Compression.ZipArchive($zipStream, [System.IO.Compression.ZipArchiveMode]::Create)
        try {
            $baseLen = $StageDir.TrimEnd('\', '/').Length + 1
            Get-ChildItem -Path $StageDir -Recurse -File | Sort-Object FullName | ForEach-Object {
                $relPath = $_.FullName.Substring($baseLen) -replace '\\', '/'
                [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                    $archive,
                    $_.FullName,
                    $relPath,
                    [System.IO.Compression.CompressionLevel]::Optimal
                ) | Out-Null
            }
        } finally {
            $archive.Dispose()
        }
    } finally {
        $zipStream.Dispose()
    }
}

function Build-WindowsMsi {
    param([string]$GoArch, [string]$StageDir, [string]$OutMsi, [string]$ProductCode)
    $msiVersion = Get-MsiNumericVersion -Ver $Version
    $wixArch = if ($GoArch -eq "amd64") { "x64" } else { "arm64" }

    $wixCmd = Get-Command wix -ErrorAction SilentlyContinue
    if (-not $wixCmd) {
        $dotnetToolWix = Join-Path $HOME ".dotnet\tools\wix.exe"
        if (Test-Path $dotnetToolWix) {
            $wixCmd = Get-Item $dotnetToolWix
        }
    }

    $candleCmd = Get-Command candle.exe -ErrorAction SilentlyContinue
    $lightCmd = Get-Command light.exe -ErrorAction SilentlyContinue
    $wixlCmd = Get-Command wixl -ErrorAction SilentlyContinue

    if ($wixCmd) {
        $wixExe = if ($wixCmd.PSObject.Properties['Source']) { $wixCmd.Source } else { $wixCmd.FullName }
        Write-Host "[INFO] Building MSI with WiX Toolset ($wixExe) for $wixArch..."
        $wxsPath = Join-Path $PackagingDir "windows\go-flow-api.wxs"
        & $wixExe build $wxsPath `
            -arch $wixArch `
            -d "StageDir=$StageDir" `
            -d "FlowMsiVersion=$msiVersion" `
            -d "FlowProductCode=$ProductCode" `
            -o $OutMsi
        if ($LASTEXITCODE -ne 0) {
            throw "wix build failed for $OutMsi"
        }
        Invoke-AuthenticodeSign $OutMsi
        return $true
    } elseif ($candleCmd -and $lightCmd) {
        Write-Host "[INFO] Building MSI with WiX v3 (candle/light) for $wixArch..."
        $wxs3Path = Join-Path $PackagingDir "windows\go-flow-api-wix3.wxs"
        $wixObj = Join-Path (Split-Path -Parent $StageDir) "go-flow-api.wixobj"
        & $candleCmd.Source -nologo -arch $wixArch `
            "-dStageDir=$StageDir" `
            "-dFlowMsiVersion=$msiVersion" `
            "-dFlowProductCode=$ProductCode" `
            -out $wixObj $wxs3Path
        if ($LASTEXITCODE -ne 0) { throw "candle.exe failed" }

        & $lightCmd.Source -nologo -sval -out $OutMsi $wixObj
        if ($LASTEXITCODE -ne 0) { throw "light.exe failed" }
        Invoke-AuthenticodeSign $OutMsi
        return $true
    } elseif ($wixlCmd) {
        Write-Host "[INFO] Building MSI with wixl (msitools) for $wixArch..."
        $wxs3Path = Join-Path $PackagingDir "windows\go-flow-api-wix3.wxs"
        & $wixlCmd.Source --arch $wixArch `
            -D "StageDir=$StageDir" `
            -D "FlowMsiVersion=$msiVersion" `
            -D "FlowProductCode=$ProductCode" `
            -o $OutMsi $wxs3Path
        if ($LASTEXITCODE -ne 0) { throw "wixl failed" }
        return $true
    } else {
        $msg = "WiX Toolset not found. Install WiX v5/v6 via 'dotnet tool install --global wix' (or 'choco install wixtoolset') to build .msi installers."
        if ($Format -eq "msi" -or $RequireMsi) {
            throw $msg
        }
        Write-Warning "$msg Skipping .msi build (portable .zip and WinGet manifests will still be generated)."
        return $false
    }
}

New-Item -ItemType Directory -Path $DistDir -Force | Out-Null

$archList = if ($Arch -eq "both") { @("amd64", "arm64") } else { @($Arch) }
$installerEntries = New-Object System.Collections.Generic.List[string]

foreach ($goArch in $archList) {
    $wingetArch = if ($goArch -eq "amd64") { "x64" } else { "arm64" }
    $workDir = Join-Path $BuildRoot "windows-$goArch"
    $stageDir = Join-Path $workDir "payload"

    Stage-WindowsPayload -GoArch $goArch -StageDir $stageDir

    if ($Format -in @("all", "zip")) {
        $zipName = "go-flow-api-$Version-windows-$goArch.zip"
        $zipPath = Join-Path $DistDir $zipName
        Build-PortableZip -StageDir $stageDir -OutZip $zipPath
        $zipSha256 = (Get-FileHash -Algorithm SHA256 -Path $zipPath).Hash.ToUpperInvariant()

        $zipBlock = @"
  - Architecture: $wingetArch
    InstallerType: zip
    NestedInstallerType: portable
    NestedInstallerFiles:
      - RelativeFilePath: bin\go-flowAPI.exe
        PortableCommandAlias: go-flowAPI
    InstallerUrl: $ReleaseBaseUrl/$zipName
    InstallerSha256: $zipSha256
"@
        $installerEntries.Add($zipBlock.TrimEnd())
    }

    if ($Format -in @("all", "msi")) {
        $msiName = "go-flow-api-$Version-windows-$goArch.msi"
        $msiPath = Join-Path $DistDir $msiName
        $productCode = Get-DeterministicProductCode -Ver $Version -GoArch $goArch
        $msiBuilt = Build-WindowsMsi -GoArch $goArch -StageDir $stageDir -OutMsi $msiPath -ProductCode $productCode
        if ($msiBuilt -and (Test-Path $msiPath)) {
            $msiSha256 = (Get-FileHash -Algorithm SHA256 -Path $msiPath).Hash.ToUpperInvariant()
            $msiBlock = @"
  - Architecture: $wingetArch
    Scope: machine
    InstallerType: wix
    InstallerUrl: $ReleaseBaseUrl/$msiName
    InstallerSha256: $msiSha256
    ProductCode: '$productCode'
    AppsAndFeaturesEntries:
      - DisplayName: ETL Madness Flow API
        Publisher: ETL Madness
        DisplayVersion: $(Get-MsiNumericVersion -Ver $Version)
        ProductCode: '$productCode'
        UpgradeCode: '$UpgradeCode'
"@
            $installerEntries.Add($msiBlock.TrimEnd())
        }
    }
}

if ($installerEntries.Count -eq 0) {
    throw "No Windows installer artifacts were produced."
}

$wingetManifestDir = Join-Path $DistDir "winget\e\ETLMadness\GoFlowAPI\$Version"
New-Item -ItemType Directory -Path $wingetManifestDir -Force | Out-Null

$versionYaml = @"
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.version.1.6.0.schema.json

PackageIdentifier: ETLMadness.GoFlowAPI
PackageVersion: $Version
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.6.0
"@

$localeYaml = @"
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.defaultLocale.1.6.0.schema.json

PackageIdentifier: ETLMadness.GoFlowAPI
PackageVersion: $Version
PackageLocale: en-US
Publisher: ETL Madness
PublisherUrl: https://github.com/etl-madness
PublisherSupportUrl: https://github.com/etl-madness/go-flowAPI/issues
PackageName: ETL Madness Flow API
PackageUrl: https://github.com/etl-madness/go-flowAPI
License: MIT
LicenseUrl: https://github.com/etl-madness/go-flowAPI/blob/main/LICENSE
ShortDescription: Centralized REST API, artifact repository, SSE execution gateway, and telemetry daemon for Flow.
Description: >-
  ETL Madness Flow API (go-flowAPI) is a pure-Go REST API and native Windows Service
  for managing Flow database artifacts (pipelines, options, configs), executing and
  validating pipelines with live Server-Sent Events (SSE) streaming, performing
  cryptographic operations, and recording audit and execution telemetry across SQL Server,
  PostgreSQL, MySQL/MariaDB, SQLite, and Oracle.
Moniker: go-flow-api
Tags:
  - api
  - daemon
  - database
  - etl
  - golang
  - rest
  - sql
  - workflow
Documentations:
  - DocumentLabel: Online Documentation
    DocumentUrl: https://etl-madness.github.io/flow-docs/
ManifestType: defaultLocale
ManifestVersion: 1.6.0
"@

$joinedInstallers = ($installerEntries -join "`n")
$installerYaml = @"
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.installer.1.6.0.schema.json

PackageIdentifier: ETLMadness.GoFlowAPI
PackageVersion: $Version
Commands:
  - go-flowAPI
Installers:
$joinedInstallers
ManifestType: installer
ManifestVersion: 1.6.0
"@

$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText((Join-Path $wingetManifestDir "ETLMadness.GoFlowAPI.yaml"), ($versionYaml -replace "`r`n", "`n") + "`n", $utf8NoBom)
[System.IO.File]::WriteAllText((Join-Path $wingetManifestDir "ETLMadness.GoFlowAPI.locale.en-US.yaml"), ($localeYaml -replace "`r`n", "`n") + "`n", $utf8NoBom)
[System.IO.File]::WriteAllText((Join-Path $wingetManifestDir "ETLMadness.GoFlowAPI.installer.yaml"), ($installerYaml -replace "`r`n", "`n") + "`n", $utf8NoBom)

Write-Host "[INFO] Generated WinGet manifests in: $wingetManifestDir"

if (-not $SkipWingetValidate) {
    $wingetCmd = Get-Command winget.exe -ErrorAction SilentlyContinue
    if ($wingetCmd) {
        Write-Host "[INFO] Validating WinGet manifests with winget validate..."
        & $wingetCmd.Source validate --manifest $wingetManifestDir
        if ($LASTEXITCODE -ne 0) {
            throw "winget validate reported manifest errors in $wingetManifestDir"
        }
    }
}
