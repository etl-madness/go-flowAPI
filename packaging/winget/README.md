# WinGet Packaging (`ETLMadness.GoFlowAPI`)

Running [`../scripts/Build-Windows.ps1`](../scripts/Build-Windows.ps1) (on Windows) or [`../scripts/build-windows.sh`](../scripts/build-windows.sh) (on Linux/macOS/FreeBSD) automatically builds Windows artifacts and generates a complete multi-file **WinGet 1.6.0** manifest directory under:

```text
packaging/dist/winget/e/ETLMadness/GoFlowAPI/<version>/
├── ETLMadness.GoFlowAPI.yaml
├── ETLMadness.GoFlowAPI.installer.yaml
└── ETLMadness.GoFlowAPI.locale.en-US.yaml
```

*(Notice the path `manifests/e/ETLMadness/GoFlowAPI/<version>/` matches the exact directory hierarchy required by [`microsoft/winget-pkgs`](https://github.com/microsoft/winget-pkgs).)*

---

## Supported Installers in the Manifest

1. **Portable `.zip` (`InstallerType: zip` + `NestedInstallerType: portable`)**
   - Always built with zero external dependencies (supports both user and machine WinGet scopes automatically).
   - Extracts into `%LOCALAPPDATA%\Microsoft\WinGet\Packages\ETLMadness.GoFlowAPI_...` (or `%ProgramFiles%\WinGet\Packages\...` with `--scope machine`), exposes `go-flowAPI` via WinGet `Links` (at the end of `PATH`), and includes `share\services\windows\Install-GoFlowAPIService.ps1` + SQL initialization scripts (`share\sql\*.sql`).

2. **Windows Installer `.msi` (`Scope: machine`, `InstallerType: wix`)**
   - Built automatically whenever WiX Toolset (`dotnet tool install --global wix`, Chocolatey `wixtoolset`, or Linux `msitools` `wixl`) is installed.
   - Installs isolated into `C:\Program Files\ETL Madness\Flow API\` (`bin\`, `docs\`, `share\sql\`, `share\services\windows\`, `share\man\`) and appends `C:\Program Files\ETL Madness\Flow API\bin` to the **end** of the machine `PATH` (`Part="last"`) so existing binaries are never replaced or shadowed.

---

## Validating, Testing & Registering the Windows Service Locally

```powershell
# 1. Build Windows .zip, .msi (if WiX installed), and WinGet manifests
.\packaging\scripts\Build-Windows.ps1 -Format all -Arch amd64

# 2. Validate WinGet manifest schema & rules
winget validate --manifest .\packaging\dist\winget\e\ETLMadness\GoFlowAPI\1.3.2\

# 3. Enable local manifest installs (one-time admin setting) & test install
winget settings --enable LocalManifestFiles
winget install --manifest .\packaging\dist\winget\e\ETLMadness\GoFlowAPI\1.3.2\

# 4. (Optional, Elevated PowerShell) Register & start the native Windows Service ('GoFlowAPI'):
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Install-GoFlowAPIService.ps1" -Start
```

---

## Prerequisites to Build `.msi`

If WiX is not yet installed on Windows, install WiX v5/v6 via the .NET SDK in one command:

```powershell
dotnet tool install --global wix
```

Or via Chocolatey:

```powershell
choco install wixtoolset -y
```

On Linux build runners, install `msitools` (`apt install msitools` or `dnf install msitools`) to cross-build the `.msi` via `wixl`.
