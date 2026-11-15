<#
.SYNOPSIS
    Installs or updates the native Windows Service for ETL Madness go-flowAPI.

.DESCRIPTION
    go-flowAPI.exe natively implements the Windows Service Control Manager (SCM)
    dispatcher (no NSSM or third-party wrapper required).
    This script:
      1. Creates C:\ProgramData\ETL Madness\Flow API\{data,logs} and seeds go-flow-api.env if missing.
      2. Grants read/modify ACLs on C:\ProgramData\ETL Madness\Flow API\ to the chosen service account.
      3. Registers (or updates) the 'GoFlowAPI' Windows Service with automatic delayed start
         and automatic failure recovery (restart after 5s / 10s / 30s).
      4. Supports NT AUTHORITY\NetworkService (default), LocalSystem, gMSA (DOMAIN\gmsa$),
         or an Active Directory domain user (DOMAIN\svc_flowapi) for Windows Integrated SQL Auth.

.EXAMPLE
    # Run from an elevated (Administrator) PowerShell prompt:
    .\Install-GoFlowAPIService.ps1 -Start

.EXAMPLE
    # Run as an Active Directory domain user for Windows Integrated SQL Server Auth:
    $cred = Get-Credential "RSYSLAB\svc_flowapi"
    .\Install-GoFlowAPIService.ps1 -Credential $cred -Dsn "sqlserver://sqlhost.rsyslab.com:1433?database=FLOW_DB&trustServerCertificate=true" -Start
#>
[CmdletBinding()]
param(
    [string]$ServiceName = "GoFlowAPI",

    [string]$DisplayName = "ETL Madness go-flowAPI Service",

    [string]$BinaryPath = "",

    [string]$ProgramDataDir = "$env:ProgramData\ETL Madness\Flow API",

    [string]$ListenAddr = ":8080",

    [string]$Dsn = "",

    [ValidateSet("NetworkService", "LocalSystem", "LocalService", "Custom")]
    [string]$BuiltInAccount = "NetworkService",

    [System.Management.Automation.PSCredential]$Credential = $null,

    [string]$GroupManagedServiceAccount = "",

    [switch]$Start
)

$ErrorActionPreference = "Stop"

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    throw "Administrator privileges are required to register or modify a Windows Service. Re-run in an elevated PowerShell prompt."
}

if ([string]::IsNullOrWhiteSpace($BinaryPath)) {
    $scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
    $relativeCandidate = Join-Path (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $scriptRoot))) "bin\go-flowAPI.exe"
    $msiCandidate = "$env:ProgramFiles\ETL Madness\Flow API\bin\go-flowAPI.exe"
    $cmdCandidate = Get-Command go-flowAPI.exe -ErrorAction SilentlyContinue

    if (Test-Path $relativeCandidate) {
        $BinaryPath = (Resolve-Path $relativeCandidate).Path
    } elseif (Test-Path $msiCandidate) {
        $BinaryPath = $msiCandidate
    } elseif ($cmdCandidate) {
        $BinaryPath = $cmdCandidate.Source
    } else {
        throw "Could not locate go-flowAPI.exe. Pass -BinaryPath 'C:\path\to\go-flowAPI.exe'."
    }
}

if (-not (Test-Path $BinaryPath)) {
    throw "Binary not found at: $BinaryPath"
}

$dataDir = Join-Path $ProgramDataDir "data"
$logsDir = Join-Path $ProgramDataDir "logs"
$envFile = Join-Path $ProgramDataDir "go-flow-api.env"
$logFile = Join-Path $logsDir "go-flow-api.log"

New-Item -ItemType Directory -Path $ProgramDataDir, $dataDir, $logsDir -Force | Out-Null

if (-not (Test-Path $envFile)) {
    $defaultSqlite = ($dataDir -replace '\\', '/') + "/flow_api.db"
    $initialDsn = if ([string]::IsNullOrWhiteSpace($Dsn)) { "sqlite://$defaultSqlite" } else { $Dsn }
    $seedEnv = @"
# ETL Madness go-flowAPI — Windows Service Environment Configuration
# Restart service after editing: Restart-Service -Name $ServiceName
FLOW_API_ADDR="$ListenAddr"
FLOW_API_DSN="$initialDsn"
FLOW_API_DEBUG="false"
# FLOW_SECURE_KEY=""
"@
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($envFile, $seedEnv, $utf8NoBom)
    Write-Host "[INFO] Created initial configuration file: $envFile"
} elseif (-not [string]::IsNullOrWhiteSpace($Dsn)) {
    Write-Host "[INFO] Existing configuration file preserved at: $envFile (edit manually to change FLOW_API_DSN)"
}

# Determine service account identity for ACLs and sc.exe
$scObj = "NT AUTHORITY\NetworkService"
$aclIdentity = "NT AUTHORITY\NETWORK SERVICE"
$scPasswordArgs = @()

if ($Credential) {
    $scObj = $Credential.UserName
    if ($scObj -notmatch '\\' -and $scObj -notmatch '@') {
        $scObj = ".\$scObj"
    }
    $aclIdentity = $scObj
    $scPasswordArgs = @("password=", $Credential.GetNetworkCredential().Password)
} elseif (-not [string]::IsNullOrWhiteSpace($GroupManagedServiceAccount)) {
    $scObj = $GroupManagedServiceAccount
    $aclIdentity = $GroupManagedServiceAccount
} else {
    switch ($BuiltInAccount) {
        "NetworkService" { $scObj = "NT AUTHORITY\NetworkService"; $aclIdentity = "NT AUTHORITY\NETWORK SERVICE" }
        "LocalService"   { $scObj = "NT AUTHORITY\LocalService";   $aclIdentity = "NT AUTHORITY\LOCAL SERVICE" }
        "LocalSystem"    { $scObj = "LocalSystem";                 $aclIdentity = "NT AUTHORITY\SYSTEM" }
    }
}

# Harden directory ACLs: Administrators, SYSTEM, and the service account
& icacls.exe $ProgramDataDir /grant "${aclIdentity}:(OI)(CI)M" | Out-Null

$binCmd = "`"$BinaryPath`" -service-name `"$ServiceName`" -env-file `"$envFile`" -log-file `"$logFile`""

$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing) {
    if ($existing.Status -ne 'Stopped') {
        Write-Host "[INFO] Stopping existing service '$ServiceName'..."
        Stop-Service -Name $ServiceName -Force
        $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(25))
    }
    Write-Host "[INFO] Updating Windows Service '$ServiceName'..."
    & sc.exe config $ServiceName binPath= $binCmd start= delayed-auto DisplayName= $DisplayName obj= $scObj @scPasswordArgs | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "sc.exe config failed with exit code $LASTEXITCODE" }
} else {
    Write-Host "[INFO] Creating Windows Service '$ServiceName'..."
    & sc.exe create $ServiceName binPath= $binCmd start= delayed-auto DisplayName= $DisplayName obj= $scObj @scPasswordArgs | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "sc.exe create failed with exit code $LASTEXITCODE" }
}

& sc.exe description $ServiceName "ETL Madness go-flowAPI — Unified Artifact CRUD, File Upload, Pipeline Execution & Telemetry REST API" | Out-Null
& sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null
& sc.exe failureflag $ServiceName 1 | Out-Null

Write-Host "[INFO] Service '$ServiceName' configured."
Write-Host "       Binary:    $BinaryPath"
Write-Host "       Account:   $scObj"
Write-Host "       Env File:  $envFile"
Write-Host "       Log File:  $logFile"

if ($Start) {
    Write-Host "[INFO] Starting service '$ServiceName'..."
    Start-Service -Name $ServiceName
    $svc = Get-Service -Name $ServiceName
    Write-Host "[INFO] Service '$ServiceName' status: $($svc.Status)"
} else {
    Write-Host "[INFO] Edit '$envFile' and start the service with: Start-Service -Name $ServiceName"
}
