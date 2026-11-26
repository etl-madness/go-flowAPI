# Flow API Packaging & OS Service Guide (`/opt/flow-api` & Windows WinGet / SCM)

This directory contains build scripts, package metadata, OS service definitions (`systemd`, OpenRC, SysVinit, FreeBSD `rc.d`, OpenBSD `rc.d`, macOS `launchd`, and native Windows Service scripts), `man` pages, GNU `info` documentation, WiX `.msi` manifests, and WinGet YAML manifests for packaging **ETL Madness Flow API (`go-flowAPI`)** across Linux (`.deb`, `.rpm` for RHEL/Fedora/SUSE/openSUSE, and `.pkg.tar.zst` for Arch Linux), macOS (`.pkg`), FreeBSD (`.pkg`), OpenBSD (`.tgz`), and Windows (`.zip` & `.msi` for WinGet).

- **Isolated Prefixes (Zero File Conflicts with System or `go-flow`)**: Unix packages install under `/opt/flow-api`, Windows `.msi` installs under `C:\Program Files\ETL Madness\Flow API\`, and WinGet portable `.zip` installs inside `%LOCALAPPDATA%\Microsoft\WinGet\Packages\ETLMadness.GoFlowAPI_...`. It can be installed side-by-side with `go-flow` (`/opt/flow`) with zero file collisions.
- **Non-Colliding Package Metadata**: OS packages use `go-flow-api` (`com.etlmadness.go-flow-api` on macOS, `ETLMadness.GoFlowAPI` on WinGet).
- **Native OS Services & `rc` Scripts Included**: Includes production-ready service definitions, unprivileged `flowapi` daemon account provisioning, preserved `go-flow-api.env` configuration files (`noreplace` / `conffiles`), and native Windows Service Control Manager (SCM) support built directly into `go-flowAPI.exe`. Services remain **disabled until you configure `FLOW_API_DSN`** and enable them.

---

## Included Assets & Install Locations

All packages compile `go-flowAPI` (`go-flowAPI.exe` on Windows) as a 100% pure-Go static executable (`CGO_ENABLED=0`) alongside SQL schema scripts, reference guides, `man`/`info` manuals, and OS service units:

| Asset | Linux (`.deb` / `.rpm` / Arch `.pkg.tar.zst`) | macOS (`.pkg`) | FreeBSD (`.pkg`) & OpenBSD (`.tgz`) | Windows (`.msi` / WinGet `.zip`) |
| :--- | :--- | :--- | :--- | :--- |
| **`go-flowAPI` binary** | `/opt/flow-api/bin/go-flowAPI` | `/opt/flow-api/bin/go-flowAPI` | `/opt/flow-api/bin/go-flowAPI` | `<Prefix>\bin\go-flowAPI.exe` |
| **Daemon Env Config** | `/etc/go-flow-api/go-flow-api.env` *(0640, preserved)* | `/opt/flow-api/etc/go-flow-api.env` *(0600, preserved)* | FreeBSD: `/usr/local/etc/go-flow-api/go-flow-api.env`<br>OpenBSD: `/etc/go-flow-api/go-flow-api.env` | `C:\ProgramData\ETL Madness\Flow API\go-flow-api.env` |
| **Primary OS Service** | `/usr/lib/systemd/system/go-flow-api.service` | `/opt/flow-api/share/services/launchd/com.etlmadness.go-flow-api.plist` | FreeBSD: `/usr/local/etc/rc.d/go_flow_api`<br>OpenBSD: `/etc/rc.d/go_flow_api` | Native SCM in `go-flowAPI.exe` + `<Prefix>\share\services\windows\*.ps1` |
| **Alternate `rc` & Kerberos Helpers** | `/opt/flow-api/share/services/{systemd,openrc,sysvinit,kerberos}/*` | `/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh` | `/opt/flow-api/share/services/{freebsd,openbsd,kerberos}/*` | — |
| **SQL Init DDL Scripts** | `/opt/flow-api/share/sql/{sqlite,postgres,sqlserver}-init.sql` | `/opt/flow-api/share/sql/*.sql` | `/opt/flow-api/share/sql/*.sql` | `<Prefix>\share\sql\*.sql` |
| **Reference Docs** | `/opt/flow-api/share/doc/*.md` & `/opt/flow-api/share/{man,info}/*` | `/opt/flow-api/share/doc/*.md` & `/opt/flow-api/share/{man,info}/*` | `/opt/flow-api/share/doc/*.md` & `/opt/flow-api/share/{man,info}/*` | `<Prefix>\docs\*.md` & `<Prefix>\share\man\man{1,5}\*` |
| **State & Log Dirs** | `/var/lib/go-flow-api` & `/var/log/go-flow-api` | `/var/db/go-flow-api` & `/var/log/go-flow-api` | `/var/db/go-flow-api` & `/var/log/go-flow-api` | `C:\ProgramData\ETL Madness\Flow API\{data,logs}` |
| **`PATH` / `MANPATH` / `INFOPATH`** | `/etc/profile.d/go-flow-api.sh` | `/private/etc/paths.d/go-flow-api` & `/private/etc/manpaths.d/go-flow-api` | FreeBSD: `/usr/local/etc/profile.d/go-flow-api.sh` & `/usr/local/etc/man.d/go-flow-api.conf`<br>OpenBSD: `/etc/profile.d/go-flow-api.sh` | MSI `Part="last"` `PATH` append / WinGet `Links` |

*(On Windows, `<Prefix>` is `C:\Program Files\ETL Madness\Flow API` for `.msi` or `%LOCALAPPDATA%\Microsoft\WinGet\Packages\ETLMadness.GoFlowAPI_...` for portable `.zip`.)*

---

## Directory Layout

```text
packaging/
├── etc/
│   ├── go-flow-api/
│   │   └── go-flow-api.env                  # Default daemon environment config template
│   └── profile.d/
│       └── go-flow-api.sh                   # POSIX sh login profile drop-in (PATH + INFOPATH)
├── services/
│   ├── systemd/
│   │   └── go-flow-api.service              # Linux systemd service unit
│   ├── openrc/
│   │   └── go-flow-api                      # Alpine / Gentoo OpenRC init script
│   ├── sysvinit/
│   │   └── go-flow-api                      # LSB POSIX SysVinit /etc/init.d script
│   ├── freebsd/
│   │   └── go_flow_api                      # FreeBSD /etc/rc.subr service script
│   ├── openbsd/
│   │   └── go_flow_api                      # OpenBSD /etc/rc.d/rc.subr service script
│   ├── launchd/
│   │   └── com.etlmadness.go-flow-api.plist # macOS LaunchDaemon plist
│   ├── kerberos/
│   │   └── go-flow-api-krb5-refresh.sh      # Bare-metal Kerberos TGT & nested AD group refresh helper
│   └── windows/
│       ├── Install-GoFlowAPIService.ps1     # Native Windows SCM service installer/updater
│       └── Uninstall-GoFlowAPIService.ps1   # Windows SCM service uninstaller
├── debian/
│   ├── postinst                             # Creates flowapi user/dirs & reloads systemd
│   ├── prerm                                # Stops & disables service before removal
│   └── postrm                               # Reloads systemd & cleans /etc/go-flow-api on purge
├── arch/
│   ├── PKGBUILD.tmpl                        # Arch Linux PKGBUILD template
│   └── go-flow-api.install                  # Pacman install/upgrade/remove hooks
├── rpm/
│   └── go-flow-api.spec                     # RPM spec file (RHEL, Fedora, SUSE, openSUSE)
├── macos/
│   └── Distribution.xml.tmpl                # macOS productbuild distribution template
├── freebsd/
│   ├── +MANIFEST.tmpl                       # FreeBSD pkg manifest template
│   ├── +POST_INSTALL                        # Creates flowapi user/dirs & seeds env config
│   └── +PRE_DEINSTALL                       # Stops go_flow_api rc.d service before removal
├── openbsd/
│   ├── +DESC                                # OpenBSD pkg_create description
│   ├── +INSTALL                             # Creates flowapi user/dirs & seeds env config
│   ├── +DEINSTALL                           # Stops go_flow_api rc.d service before removal
│   └── PLIST.tmpl                           # OpenBSD pkg_create packing list template
├── windows/
│   ├── go-flow-api.wxs                      # WiX Toolset v4/v5/v6 MSI manifest
│   └── go-flow-api-wix3.wxs                 # WiX v3 / Linux msitools (wixl) MSI manifest
├── winget/
│   └── README.md                            # WinGet validation, local install & publishing guide
├── man/
│   ├── man1/go-flowAPI.1                    # go-flowAPI(1) CLI & daemon manual page
│   └── man5/go-flow-api.env.5               # go-flow-api.env(5) configuration manual page
├── info/
│   ├── go-flow-api.texi                     # GNU Texinfo source manual
│   ├── go-flow-api.info                     # Compiled GNU Info manual (with Tag Table)
│   └── dir                                  # Self-contained GNU Info directory index
├── nfpm.yaml                                # Optional nFPM fallback config (.deb / .rpm / Arch)
└── scripts/
    ├── common.sh                            # Shared POSIX sh helpers & Go cross-compilation
    ├── generate-docs.sh                     # Recompiles .texi -> .info if makeinfo exists
    ├── build-all.sh                         # Unified entrypoint for host or specific targets
    ├── build-deb.sh                         # Builds Linux .deb (dpkg-deb or nfpm)
    ├── build-rpm.sh                         # Builds Linux .rpm (rpmbuild or nfpm)
    ├── build-arch.sh                        # Builds Arch Linux .pkg.tar.zst (makepkg or nfpm)
    ├── build-macos-pkg.sh                   # Builds macOS .pkg (pkgbuild + productbuild)
    ├── build-freebsd-pkg.sh                 # Builds FreeBSD .pkg (pkg create)
    ├── build-openbsd-pkg.sh                 # Builds OpenBSD .tgz (pkg_create or cross-compiled tar)
    ├── build-windows.sh                     # Cross-builds Windows .zip/.msi & WinGet YAML from Unix
    ├── Build-Windows.ps1                    # Native Windows .zip/.msi & WinGet manifest builder
    └── Build-Packages.ps1                   # PowerShell cross-compile & staging helper
```

---

## Building Packages

Output artifacts are written to `packaging/dist/`. All shell scripts are portable POSIX `/bin/sh` scripts.

> **Custom Build-Time Schema & Table Names (`FLOW_EXTRA_LDFLAGS`):**
> To bake a custom default database schema or table names into any OS package without editing source files, **`export FLOW_EXTRA_LDFLAGS`** or pass it **inline on the same command line** as the build script (setting `FLOW_EXTRA_LDFLAGS=...` on a separate line without `export` will not be inherited by child `sh` scripts). You can also use `-tables-config` / `FLOW_TABLES_CONFIG` at runtime and `-print-ddl <dialect>` to generate matching SQL:
> ```sh
> # Exported before running the build script:
> export FLOW_EXTRA_LDFLAGS="-X github.com/etl-madness/flow.DefaultSchema=etl_flow"
> sh ./packaging/scripts/build-all.sh host
>
> # Or passed inline on the same command line (e.g. FreeBSD):
> FREEBSD_MAJOR=15 FLOW_EXTRA_LDFLAGS="-X github.com/etl-madness/flow.DefaultSchema=etl_flow" \
>   sh ./packaging/scripts/build-freebsd-pkg.sh amd64
> ```

### 1. Build for Current Host OS
```sh
sh ./packaging/scripts/build-all.sh host
```

### 2. Linux Debian / Ubuntu (`.deb`)
Requires `dpkg-deb` (or `nfpm`):
```sh
sh ./packaging/scripts/build-deb.sh amd64
sh ./packaging/scripts/build-deb.sh arm64
```

### 3. Linux RHEL / Fedora / SUSE / openSUSE (`.rpm`)
Requires `rpmbuild` (`rpm-build` package) or `nfpm`:
```sh
sh ./packaging/scripts/build-rpm.sh amd64
sh ./packaging/scripts/build-rpm.sh arm64
```

### 4. Arch Linux (`.pkg.tar.zst`)
Requires `makepkg` (`pacman`, run as a non-root user) or `nfpm`:
```sh
sh ./packaging/scripts/build-arch.sh amd64
sh ./packaging/scripts/build-arch.sh arm64
```

### 5. macOS Installer (`.pkg`)
Run on macOS with Xcode Command Line Tools (`pkgbuild` and `productbuild`). Supports `arm64`, `amd64`, or `universal` (fat binary via `lipo`):
```sh
sh ./packaging/scripts/build-macos-pkg.sh arm64
sh ./packaging/scripts/build-macos-pkg.sh universal

# Optional Developer ID signing:
MACOS_SIGN_IDENTITY="Developer ID Installer: Your Name (TEAMID)" \
  sh ./packaging/scripts/build-macos-pkg.sh universal
```

### 6. FreeBSD Package (`.pkg`)
Run on FreeBSD (`pkg create`):
```sh
sh ./packaging/scripts/build-freebsd-pkg.sh amd64
FREEBSD_MAJOR=14 sh ./packaging/scripts/build-freebsd-pkg.sh arm64
```

### 6b. OpenBSD Package (`.tgz`)
Run on OpenBSD (`/usr/sbin/pkg_create`; outputs `packaging/dist/openbsd-<arch>/go-flow-api-<version>.tgz` to satisfy OpenBSD `packages-specs(7)`), or cross-compile a root bundle off-host:
```sh
sh ./packaging/scripts/build-openbsd-pkg.sh amd64
sh ./packaging/scripts/build-openbsd-pkg.sh arm64

# Optional signify(1) signing on OpenBSD:
OPENBSD_SIGNIFY_KEY="/etc/signify/my-pkg.sec" \
  sh ./packaging/scripts/build-openbsd-pkg.sh amd64
```

### 7. Windows (`.zip` & `.msi`) + WinGet (`ETLMadness.GoFlowAPI`)
On Windows PowerShell (builds `.zip` with zero extra tools, builds `.msi` into `C:\Program Files\ETL Madness\Flow API\` when WiX Toolset is installed via `dotnet tool install --global wix`, generates WinGet 1.6.0 manifests in `packaging/dist/winget/`, and runs `winget validate`):
```powershell
.\packaging\scripts\Build-Windows.ps1 -Format all -Arch amd64
# Or build both x64 and arm64:
.\packaging\scripts\Build-Windows.ps1 -Format all -Arch both
```

From Linux/macOS/FreeBSD (cross-compiles `.zip`, builds `.msi` if `wixl`/`wix` is installed, and generates WinGet YAML manifests):
```sh
sh ./packaging/scripts/build-windows.sh amd64
```

See [winget/README.md](winget/README.md) for local `winget install --manifest` testing and Authenticode code signing.

---

## Installing & Uninstalling Packages by OS / Format

### 1. Debian / Ubuntu (`.deb`)

```sh
# Install (creates 'flowapi' system user, /etc/go-flow-api/go-flow-api.env, and systemd unit)
sudo apt install ./packaging/dist/go-flow-api_1.3.2-1_amd64.deb
# Or directly with dpkg:
sudo dpkg -i ./packaging/dist/go-flow-api_1.3.2-1_amd64.deb

# Activate PATH & INFOPATH in the current shell
. /etc/profile.d/go-flow-api.sh

# Verify binary & manuals
/opt/flow-api/bin/go-flowAPI -h
man go-flowAPI
man 5 go-flow-api.env

# Uninstall ('remove' keeps /etc/go-flow-api/go-flow-api.env; 'purge' deletes it)
sudo apt remove go-flow-api
sudo apt purge go-flow-api
```

### 2. RHEL / Fedora / Rocky / AlmaLinux (`.rpm`)

```sh
# Install or upgrade
sudo dnf install ./packaging/dist/go-flow-api-1.3.2-1.x86_64.rpm
# Or directly with rpm:
sudo rpm -Uvh ./packaging/dist/go-flow-api-1.3.2-1.x86_64.rpm

. /etc/profile.d/go-flow-api.sh
go-flowAPI -h
man go-flowAPI

# Uninstall
sudo dnf remove go-flow-api
```

### 3. SUSE Linux Enterprise (SLES) & openSUSE (`.rpm`)

```sh
sudo zypper --no-gpg-checks install ./packaging/dist/go-flow-api-1.3.2-1.x86_64.rpm

. /etc/profile.d/go-flow-api.sh
go-flowAPI -h

# Uninstall
sudo zypper remove go-flow-api
```

### 4. Arch Linux / Manjaro (`.pkg.tar.zst`)

```sh
sudo pacman -U ./packaging/dist/go-flow-api-1.3.2-1-x86_64.pkg.tar.zst

. /etc/profile.d/go-flow-api.sh
go-flowAPI -h

# Uninstall
sudo pacman -Rns go-flow-api
```

### 5. macOS (`.pkg`)

**Graphical Install:** Double-click `go-flow-api-<version>-darwin-universal.pkg`.

**Command-Line Install:**
```sh
sudo installer -pkg ./packaging/dist/go-flow-api-1.3.2-darwin-universal.pkg -target /
eval "$(/usr/libexec/path_helper -s)"
go-flowAPI -h
man go-flowAPI

# Uninstall (stop LaunchDaemon first if enabled)
sudo launchctl bootout system/com.etlmadness.go-flow-api 2>/dev/null || true
sudo rm -f /Library/LaunchDaemons/com.etlmadness.go-flow-api.plist
sudo rm -rf /opt/flow-api \
            /private/etc/paths.d/go-flow-api \
            /private/etc/manpaths.d/go-flow-api
sudo pkgutil --forget com.etlmadness.go-flow-api
```

### 6. FreeBSD (`.pkg`)

```sh
sudo pkg add ./packaging/dist/go-flow-api-1.3.2-freebsd-amd64.pkg

. /usr/local/etc/profile.d/go-flow-api.sh
go-flowAPI -h
man go-flowAPI

# Uninstall (automatically stops go_flow_api service before removal)
sudo pkg delete go-flow-api
```

### 6b. OpenBSD (`.tgz`)

```sh
# Install native pkg_create package on OpenBSD (-D unsigned for unsigned local packages; omit if signed)
doas pkg_add -D unsigned ./packaging/dist/openbsd-amd64/go-flow-api-1.3.2.tgz

# (Or if installing a cross-compiled bundle built off-host:)
doas tar -xzphf ./packaging/dist/go-flow-api-1.3.2-openbsd-amd64-bundle.tar.gz -C /
doas sh /opt/flow-api/share/services/openbsd/+INSTALL go-flow-api-1.3.2 POST-INSTALL

# Activate PATH, MANPATH, and INFOPATH in OpenBSD ksh
. /etc/profile.d/go-flow-api.sh
go-flowAPI -h
man go-flowAPI

# Enable & start via rcctl(8)
doas rcctl enable go_flow_api
doas rcctl start go_flow_api
doas rcctl check go_flow_api

# Uninstall
doas pkg_delete go-flow-api
```

### 7. Windows — WinGet, Standalone `.msi`, or Portable `.zip`

```powershell
# WinGet (once published, or via --manifest .\packaging\dist\winget\e\ETLMadness\GoFlowAPI\1.3.2\)
winget install --id ETLMadness.GoFlowAPI
winget install --id ETLMadness.GoFlowAPI --scope machine --installer-type wix

# Standalone silent MSI install into C:\Program Files\ETL Madness\Flow API\
msiexec /i .\packaging\dist\go-flow-api-1.3.2-windows-amd64.msi /qn /norestart

# Uninstall MSI (remove Windows Service first if registered)
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Uninstall-GoFlowAPIService.ps1"
msiexec /x .\packaging\dist\go-flow-api-1.3.2-windows-amd64.msi /qn /norestart
```

---

### 8. Running Outside Docker with Kerberos Tickets (`klist`) & Active Directory Permissions

In Docker deployments (`docker-compose.ad-sidecar.yml`), a companion `krb5-sidecar` container acquires a Kerberos Ticket-Granting Ticket (`kinit` / `klist`), resolves nested Active Directory groups via `ldapsearch -Y GSSAPI`, and shares `/krb5cache` with `go-flowAPI`.

When running an installed OS package **directly on a host or VM outside Docker**, `go-flowAPI` uses the host's Kerberos credential cache (`KRB5CCNAME`), a Kerberos keytab (`KRB5_KTNAME`), or native Windows SSPI directly—both for the startup `FLOW_API_DSN` database and for in-process pipeline `<Database>` connections—while Unix packages bundle `/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh` to automate host TGT renewal and nested AD group lookups.

#### A. Linux, FreeBSD, OpenBSD & macOS — Interactive Execution with `kinit` / `klist`

1. **Install Kerberos client tools (and optional OpenLDAP GSSAPI tools for dynamic AD group lookup):**
   ```sh
   # Debian / Ubuntu
   sudo apt install krb5-user ldap-utils libsasl2-modules-gssapi-mit

   # RHEL / Fedora / Rocky / AlmaLinux
   sudo dnf install krb5-workstation openldap-clients cyrus-sasl-gssapi

   # SLES / openSUSE
   sudo zypper install krb5-client openldap2-client cyrus-sasl-gssapi

   # Arch Linux
   sudo pacman -S krb5 libldap cyrus-sasl-gssapi

   # FreeBSD (macOS ships kinit/klist out of the box)
   sudo pkg install krb5 openldap26-client cyrus-sasl-gssapi

   # OpenBSD (uses Heimdal in /usr/local/heimdal/bin + GSSAPI-flavored OpenLDAP & Cyrus SASL)
   doas pkg_add heimdal openldap-client--gssapi cyrus-sasl--gssapi
   ```

2. **Configure `/etc/krb5.conf` for your Active Directory realm and KDC (TCP/UDP port `88` must be reachable):**
   ```ini
   [libdefaults]
       default_realm = EXAMPLE.COM
       dns_lookup_realm = false
       dns_lookup_kdc = true
       ticket_lifetime = 24h
       renew_lifetime = 7d
       forwardable = true
       rdns = false

   [realms]
       EXAMPLE.COM = {
           kdc = dc.example.com
           admin_server = dc.example.com
           default_domain = example.com
       }

   [domain_realm]
       .example.com = EXAMPLE.COM
       example.com = EXAMPLE.COM
   ```

3. **Acquire a `FILE:` Kerberos ticket cache and verify with `klist -f`:**
   > **Important (`FILE:` Credential Cache Requirement):** Modern Linux distros with SSSD often default `KRB5CCNAME` to `KCM:` or `KEYRING:persistent:%{uid}`, and macOS defaults to `API:`. Pure-Go `gokrb5` requires a standard MIT **`FILE:`** credential cache (and `go-flowAPI` automatically strips any `FILE:` prefix). Always point `KRB5CCNAME` to a file path before running `kinit`:

   ```sh
   export KRB5CCNAME="FILE:/tmp/krb5cc_$(id -u)"
   export KRB5_CONFIG="/etc/krb5.conf"
   export AD_REALM="EXAMPLE.COM"

   # Acquire TGT for your AD user and inspect ticket flags
   kinit svc_goflow@EXAMPLE.COM
   klist -f
   ```

4. **Resolve Active Directory user & `<group>` RBAC permissions (Static or Dynamic LDAP GSSAPI):**
   ```sh
   export FLOW_WINDOWS_USER='EXAMPLE\svc_goflow'

   # Option 1: Static comma-separated AD groups
   export FLOW_WINDOWS_GROUPS='EXAMPLE\ETL_Admins,EXAMPLE\Flow_Runners'

   # Option 2: Dynamic nested AD group sync via bundled helper (uses ldapsearch -Y GSSAPI)
   AD_DOMAIN="EXAMPLE" \
   AD_REALM="EXAMPLE.COM" \
   AD_USER="svc_goflow" \
   AD_DC_HOST="dc.example.com" \
   AD_LDAP_BASE_DN="DC=example,DC=com" \
   KRB5CCNAME="$KRB5CCNAME" \
   FLOW_WINDOWS_GROUPS_FILE="/tmp/ad-groups-$(id -u).txt" \
     /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once
   export FLOW_WINDOWS_GROUPS_FILE="/tmp/ad-groups-$(id -u).txt"
   ```

5. **Start `go-flowAPI` using Kerberos Integrated SQL Server Authentication:**
   *(Ensure your SQL Server hostname has a valid `MSSQLSvc/<fqdn>:1433` SPN registered in AD, or append `&ServerSPN=MSSQLSvc/sqlhost.example.com:1433` when connecting through a DNS CNAME/alias.)*
   ```sh
   export FLOW_API_DSN="sqlserver://db.example.com:1433?database=EXAMPLE_DB&integrated+security=true&authenticator=krb5&trustServerCertificate=true"
   /opt/flow-api/bin/go-flowAPI -addr :8080
   ```

#### B. Linux, FreeBSD, OpenBSD & macOS — Unattended Daemon Mode (Keytab or Host Renewal Timer)

For background OS services (`systemd`, OpenRC, SysVinit, FreeBSD `rc.d`, OpenBSD `rc.d`, macOS `launchd`) running as `flowapi` without interactive password prompts:

1. **Generate a keytab on a domain-joined Windows admin workstation (or via `ktutil`) and secure it on the Unix host:**
   ```powershell
   # On Windows AD admin host:
   ktpass /out flowapi.keytab /princ svc_goflow@EXAMPLE.COM /pass "YourADPassword" /crypto AES256-SHA1 /ptype KRB5_NT_PRINCIPAL
   ```
   ```sh
   # On Linux / FreeBSD / macOS host (use 'doas' on OpenBSD):
   sudo install -o root -g flowapi -m 0640 ./flowapi.keytab /etc/go-flow-api/flowapi.keytab
   ```

2. **Choose Direct Keytab Auth OR Periodic `klist` Cache + Nested AD Group Sync in `go-flow-api.env`:**

   - **Mode 1 — Direct Keytab Authentication (No Cron/Timer Required):**
     `go-flowAPI` reads the keytab directly via `KRB5_KTNAME` and derives the principal username from `AD_USER` / `FLOW_WINDOWS_USER`:
     ```dotenv
     # /etc/go-flow-api/go-flow-api.env (or /usr/local/etc/go-flow-api/go-flow-api.env on FreeBSD)
     FLOW_API_ADDR=":8080"
     FLOW_API_DSN="sqlserver://db.example.com:1433?database=EXAMPLE_DB&integrated+security=true&authenticator=krb5&trustServerCertificate=true"
     AD_REALM="EXAMPLE.COM"
     AD_USER="svc_goflow"
     KRB5_CONFIG="/etc/krb5.conf"
     KRB5_KTNAME="/etc/go-flow-api/flowapi.keytab"
     FLOW_WINDOWS_USER="EXAMPLE\svc_goflow"
     FLOW_WINDOWS_GROUPS="EXAMPLE\ETL_Admins,EXAMPLE\Flow_Runners"
     ```

   - **Mode 2 — Managed `klist` Ticket Cache + Dynamic LDAP Nested Group Refresh (Bare-Metal Sidecar Equivalent):**
     Use the bundled `go-flow-api-krb5-refresh.sh` script to maintain `/var/lib/go-flow-api/krb5cc` (`/var/db/go-flow-api/krb5cc` on OpenBSD/FreeBSD, enforced at mode `0600` as required by Heimdal and owned by `flowapi`) and refresh nested AD groups in `ad-groups.txt`:
     ```dotenv
     # /etc/go-flow-api/go-flow-api.env (use /var/db/go-flow-api/... on OpenBSD & FreeBSD)
     FLOW_API_ADDR=":8080"
     FLOW_API_DSN="sqlserver://db.example.com:1433?database=EXAMPLE_DB&integrated+security=true&authenticator=krb5&trustServerCertificate=true"
     AD_DOMAIN="EXAMPLE"
     AD_REALM="EXAMPLE.COM"
     AD_USER="svc_goflow"
     AD_DC_HOST="dc.example.com"
     AD_LDAP_BASE_DN="DC=example,DC=com"
     KRB5_CONFIG="/etc/krb5.conf"
     KRB5_KTNAME="/etc/go-flow-api/flowapi.keytab"
     KRB5CCNAME="/var/lib/go-flow-api/krb5cc"
     FLOW_WINDOWS_USER="EXAMPLE\svc_goflow"
     FLOW_WINDOWS_GROUPS_FILE="/var/lib/go-flow-api/ad-groups.txt"
     ```
     Seed the initial cache/groups **as `flowapi`** (so the `0600` ccache is owned by `flowapi`) and schedule periodic 4-hour renewal:
     ```sh
     # Linux / FreeBSD: initial ticket + nested AD group sync as 'flowapi'
     sudo -u flowapi GO_FLOW_API_ENV_FILE=/etc/go-flow-api/go-flow-api.env \
       /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once

     # OpenBSD: initial ticket + nested AD group sync as 'flowapi' (0600 on /var/db/go-flow-api/krb5cc)
     doas -u flowapi GO_FLOW_API_ENV_FILE=/etc/go-flow-api/go-flow-api.env \
       /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once

     # Linux cron (/etc/cron.d/go-flow-api-krb5):
     echo '0 */4 * * * flowapi GO_FLOW_API_ENV_FILE=/etc/go-flow-api/go-flow-api.env /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once >/var/log/go-flow-api/krb5-refresh.log 2>&1' \
       | sudo tee /etc/cron.d/go-flow-api-krb5 >/dev/null

     # OpenBSD flowapi crontab (OpenBSD cron does not use /etc/cron.d):
     echo '0 */4 * * * GO_FLOW_API_ENV_FILE=/etc/go-flow-api/go-flow-api.env /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once >/var/log/go-flow-api/krb5-refresh.log 2>&1' \
       | doas crontab -u flowapi -
     ```

#### C. Windows (Native Outside Docker — Windows SSPI `klist` & Token Group Resolution)

On a domain-joined Windows workstation or Windows Server, `go-flowAPI.exe` uses native **Windows SSPI** for Kerberos/Integrated SQL Server authentication and resolves the active account's `DOMAIN\<Group>` SIDs directly from the Windows security token (`win_groups_windows.go`)—no `krb5.conf`, keytab, or `ad-groups.txt` file is needed:

> **Windows DSN Note:** Omit `authenticator=krb5` on native Windows so `go-mssqldb` uses the native Windows SSPI Kerberos provider (`winsspi`) rather than looking for `/etc/krb5.conf`.

```powershell
# 1. Interactive mode (runs under your current domain session; check tickets with Windows klist)
klist
$env:FLOW_API_DSN = "sqlserver://db.example.com:1433?database=EXAMPLE_DB&integrated+security=true&trustServerCertificate=true"
& "C:\Program Files\ETL Madness\Flow API\bin\go-flowAPI.exe" -addr :8080

# 2. Background Windows Service mode (runs as AD user EXAMPLE\svc_goflow or a gMSA EXAMPLE\gmsa_flowapi$)
$cred = Get-Credential "EXAMPLE\svc_goflow"
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Install-GoFlowAPIService.ps1" `
    -Credential $cred `
    -Dsn "sqlserver://db.example.com:1433?database=EXAMPLE_DB&integrated+security=true&trustServerCertificate=true" `
    -Start
```

#### D. Granting AD User / `<group>` Permissions in `dbo.flow_api_users` & Verifying

1. **Authorize your AD user or AD security groups in `dbo.flow_api_users`** using `__WINDOWS_INTEGRATED__` (highest matching role wins: `all` > `write` > `upload` > `execution-only` > `read-only`):
   ```sql
   -- Direct AD user mapping
   INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
   VALUES ('EXAMPLE\svc_goflow', '__WINDOWS_INTEGRATED__', 'all', 1, 'AD Service Principal');

   -- Or AD security group mappings (sticky <group> RBAC)
   INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
   VALUES ('EXAMPLE\ETL_Admins', '__WINDOWS_INTEGRATED__', 'all', 1, 'ETL Administrators AD Group');

   INSERT INTO dbo.flow_api_users (username, password_hash, permission, is_active, description)
   VALUES ('EXAMPLE\Flow_Runners', '__WINDOWS_INTEGRATED__', 'execution-only', 1, 'Pipeline Runners AD Group');
   ```

2. **Verify the SQL Server Kerberos session login (`db_user`) and call endpoints as the AD identity:**
   ```sh
   # Confirm db_user reports EXAMPLE\svc_goflow (not an empty or SQL fallback user)
   curl -s http://localhost:8080/api/v1/health

   # Authenticate API calls via X-Flow-Windows-User: __AUTO_HOST__
   curl -H "X-Flow-Windows-User: __AUTO_HOST__" http://localhost:8080/api/v1/artifacts/pipelines

   # Or via HTTP BasicAuth (works directly in Swagger UI at http://localhost:8080/swagger/)
   curl -u "__AUTO_HOST__:__WINDOWS_INTEGRATED__" \
     -X POST http://localhost:8080/api/v1/db/test \
     -H "Content-Type: application/json" -d '{}'
   ```

---

## Configuring & Managing OS Services (`systemd`, OpenRC, SysVinit, FreeBSD `rc.d`, `launchd`, Windows SCM)

### Step 0: Database Schema & Initial Admin User Prerequisite

`go-flowAPI` does **not** auto-create database tables at startup. Before pointing the background service at a new database:

1. **Provision tables** using `/opt/flow-api/share/doc/SCHEMA.md` or the bundled DDL scripts in `/opt/flow-api/share/sql/` (`C:\Program Files\ETL Madness\Flow API\share\sql\` on Windows):
   ```sh
   # Quick local SQLite initialization example (Linux):
   sudo -u flowapi sqlite3 /var/lib/go-flow-api/flow_api.db < /opt/flow-api/share/sql/sqlite-init.sql
   ```
2. **Seed an initial `all`-permission BasicAuth admin user** either via SQL (`/opt/flow-api/share/doc/BOOTSTRAP.md` + `go-flowAPI -hash-password 'YourStrongPass!'`) or by running a one-time bootstrap command:
   ```sh
   sudo -u flowapi /opt/flow-api/bin/go-flowAPI \
     -env-file /etc/go-flow-api/go-flow-api.env \
     -bootstrap-user admin \
     -bootstrap-pass 'ChangeMe!StrongAdminPass123'
   ```

---

### 1. Linux — `systemd` (Debian, Ubuntu, RHEL, Fedora, Rocky, Alma, SLES, openSUSE, Arch)

The `.deb`, `.rpm`, and Arch packages automatically create the unprivileged `flowapi` user/group, `/var/lib/go-flow-api`, `/etc/go-flow-api/go-flow-api.env` (`0640`), and `/usr/lib/systemd/system/go-flow-api.service`.

```sh
# 1. Edit database DSN, listen address (:8080), and optional FLOW_SECURE_KEY / Kerberos settings
sudo vi /etc/go-flow-api/go-flow-api.env
sudo chown root:flowapi /etc/go-flow-api/go-flow-api.env
sudo chmod 0640 /etc/go-flow-api/go-flow-api.env

# 2. Enable at boot and start immediately
sudo systemctl enable --now go-flow-api.service

# 3. Check health & live logs
sudo systemctl status go-flow-api.service
curl -s http://localhost:8080/api/v1/health
sudo journalctl -u go-flow-api.service -f

# 4. Restart or stop
sudo systemctl restart go-flow-api.service
sudo systemctl stop go-flow-api.service
```

> **Active Directory / Kerberos SQL Server Auth under `systemd`:** Place a service keytab at `/etc/go-flow-api/flowapi.keytab` (`chown root:flowapi`, `chmod 0640`) and set `AD_REALM="EXAMPLE.COM"` and `KRB5_KTNAME="/etc/go-flow-api/flowapi.keytab"` (or `KRB5CCNAME` from a ticket-renewal unit) inside `/etc/go-flow-api/go-flow-api.env`.

---

### 2. Linux — OpenRC (`rc-service` / `rc-update` for Alpine, Gentoo, Artix)

```sh
# 1. Ensure the unprivileged flowapi user and directories exist
sudo addgroup -S flowapi 2>/dev/null || true
sudo adduser -S -D -H -h /var/lib/go-flow-api -s /sbin/nologin -G flowapi flowapi 2>/dev/null || true
sudo install -d -o flowapi -g flowapi -m 0750 /var/lib/go-flow-api /var/log/go-flow-api

# 2. Configure /etc/go-flow-api/go-flow-api.env
sudo vi /etc/go-flow-api/go-flow-api.env
sudo chown root:flowapi /etc/go-flow-api/go-flow-api.env
sudo chmod 0640 /etc/go-flow-api/go-flow-api.env

# 3. Install the bundled OpenRC script & enable in the default runlevel
sudo install -m 0755 /opt/flow-api/share/services/openrc/go-flow-api /etc/init.d/go-flow-api
sudo rc-update add go-flow-api default
sudo rc-service go-flow-api start

# 4. Check status & logs
sudo rc-service go-flow-api status
sudo tail -f /var/log/go-flow-api/go-flow-api.log
```

---

### 3. Linux — SysVinit (`/etc/init.d` for Devuan / Legacy Init Systems)

```sh
# 1. Configure /etc/go-flow-api/go-flow-api.env
sudo vi /etc/go-flow-api/go-flow-api.env

# 2. Install the LSB SysVinit script
sudo install -m 0755 /opt/flow-api/share/services/sysvinit/go-flow-api /etc/init.d/go-flow-api

# 3. Register across runlevels (Debian/Devuan update-rc.d or RHEL chkconfig)
sudo update-rc.d go-flow-api defaults   # or: sudo chkconfig --add go-flow-api

# 4. Start, check status, and inspect logs
sudo service go-flow-api start
sudo service go-flow-api status
sudo tail -f /var/log/go-flow-api/go-flow-api.log
```

---

### 4. FreeBSD — `rc.d` (`/usr/local/etc/rc.d/go_flow_api` & `/etc/rc.conf`)

The FreeBSD `.pkg` installs `/usr/local/etc/rc.d/go_flow_api` (supervised by FreeBSD `/usr/sbin/daemon` with automatic 5-second crash recovery), creates the `flowapi` user/group, and seeds `/usr/local/etc/go-flow-api/go-flow-api.env`.

```sh
# 1. Configure database DSN and settings (initialize SQLite first if using default SQLite path)
sudo -u flowapi sqlite3 /var/db/go-flow-api/flow_api.db < /opt/flow-api/share/sql/sqlite-init.sql
sudo vi /usr/local/etc/go-flow-api/go-flow-api.env
sudo chmod 0640 /usr/local/etc/go-flow-api/go-flow-api.env

# 2. Enable in /etc/rc.conf via sysrc
sudo sysrc go_flow_api_enable="YES"

# Optional rc.conf overrides (defaults shown):
# sudo sysrc go_flow_api_user="flowapi"
# sudo sysrc go_flow_api_group="flowapi"
# sudo sysrc go_flow_api_env_file="/usr/local/etc/go-flow-api/go-flow-api.env"
# sudo sysrc go_flow_api_logfile="/var/log/go-flow-api/go-flow-api.log"
# sudo sysrc go_flow_api_flags="-data-dir /var/db/go-flow-api/jobs"
# sudo sysrc go_flow_api_env="PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

# 3. Start, inspect status, and tail logs
sudo service go_flow_api start
sudo service go_flow_api status
sudo tail -f /var/log/go-flow-api/go-flow-api.log

# 4. Restart or stop
sudo service go_flow_api restart
sudo service go_flow_api stop
```

---

### 4b. OpenBSD — `rc.d` (`/etc/rc.d/go_flow_api` & `rcctl`)

The OpenBSD `.tgz` installs `/etc/rc.d/go_flow_api`, creates the unprivileged `flowapi` user/group, and seeds `/etc/go-flow-api/go-flow-api.env` (`0640`).

```sh
# 1. Configure database DSN and settings (initialize SQLite first if using default SQLite path)
doas -u flowapi sqlite3 /var/db/go-flow-api/flow_api.db < /opt/flow-api/share/sql/sqlite-init.sql
doas vi /etc/go-flow-api/go-flow-api.env
doas chown root:flowapi /etc/go-flow-api/go-flow-api.env
doas chmod 0640 /etc/go-flow-api/go-flow-api.env

# 2. Enable in /etc/rc.conf.local and start via rcctl(8)
doas rcctl enable go_flow_api
doas rcctl start go_flow_api

# 3. Inspect status & tail logs
doas rcctl check go_flow_api
doas tail -f /var/log/go-flow-api/go-flow-api.log

# 4. Restart or stop
doas rcctl restart go_flow_api
doas rcctl stop go_flow_api
```

---

### 5. macOS — `launchd` (`LaunchDaemon` at Boot)

Because `/Library/LaunchDaemons/*.plist` files must be world-readable (`0644`), the bundled plist invokes `go-flowAPI -env-file /opt/flow-api/etc/go-flow-api.env` so database credentials remain protected inside a `0600` file:

```sh
# 1. Edit /opt/flow-api/etc/go-flow-api.env and lock down permissions
sudo vi /opt/flow-api/etc/go-flow-api.env
sudo chown root:wheel /opt/flow-api/etc/go-flow-api.env
sudo chmod 0600 /opt/flow-api/etc/go-flow-api.env

# 2. Install the LaunchDaemon plist and load into the system domain
sudo cp /opt/flow-api/share/services/launchd/com.etlmadness.go-flow-api.plist /Library/LaunchDaemons/
sudo chown root:wheel /Library/LaunchDaemons/com.etlmadness.go-flow-api.plist
sudo chmod 0644 /Library/LaunchDaemons/com.etlmadness.go-flow-api.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.etlmadness.go-flow-api.plist

# 3. Check service state & logs
sudo launchctl print system/com.etlmadness.go-flow-api
tail -f /var/log/go-flow-api/go-flow-api.log

# 4. Restart after editing /opt/flow-api/etc/go-flow-api.env, or unload
sudo launchctl kickstart -k system/com.etlmadness.go-flow-api
sudo launchctl bootout system/com.etlmadness.go-flow-api
```

---

### 6. Windows — Native Windows Service (`GoFlowAPI` / SCM)

`go-flowAPI.exe` natively implements the Windows Service Control Manager dispatcher (`golang.org/x/sys/windows/svc`) — **no NSSM or third-party wrapper executable is required**.

Run the bundled PowerShell helper from an **elevated (Administrator) PowerShell** prompt:

```powershell
# Option A: Default install (NT AUTHORITY\NetworkService, config & logs in C:\ProgramData\ETL Madness\Flow API\)
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Install-GoFlowAPIService.ps1" `
    -Dsn "sqlserver://flow_api:YourStrong!Pass@sqlhost:1433?database=FLOW_DB&trustServerCertificate=true" `
    -ListenAddr ":8080" `
    -Start

# Option B: Run as an Active Directory Domain Account for Windows Integrated SQL Server Auth
# (Ensure the AD user has 'Log on as a service' rights on the Windows host)
$cred = Get-Credential "RSYSLAB\svc_flowapi"
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Install-GoFlowAPIService.ps1" `
    -Credential $cred `
    -Dsn "sqlserver://sqlhost.rsyslab.com:1433?database=FLOW_DB&trustServerCertificate=true" `
    -Start

# Option C: Run as a Group Managed Service Account (gMSA)
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Install-GoFlowAPIService.ps1" `
    -GroupManagedServiceAccount "RSYSLAB\gmsa_flowapi$" `
    -Dsn "sqlserver://sqlhost.rsyslab.com:1433?database=FLOW_DB&trustServerCertificate=true" `
    -Start
```

**Managing & Uninstalling the Windows Service:**
```powershell
# Inspect status & tail log file
Get-Service -Name GoFlowAPI
Get-Content "C:\ProgramData\ETL Madness\Flow API\logs\go-flow-api.log" -Tail 50 -Wait

# Restart after editing C:\ProgramData\ETL Madness\Flow API\go-flow-api.env
Restart-Service -Name GoFlowAPI
Stop-Service -Name GoFlowAPI

# Remove service (add -PurgeData to also delete C:\ProgramData\ETL Madness\Flow API)
& "C:\Program Files\ETL Madness\Flow API\share\services\windows\Uninstall-GoFlowAPIService.ps1"
```

---

## Viewing Documentation After Installation

Open a new login shell (so `/opt/flow-api/bin` and `INFOPATH` are active) or invoke directly:

```sh
# Unix Manual pages
man go-flowAPI
man 5 go-flow-api.env

# Unix GNU Info pages
info go-flow-api
```

On Windows, reference guides and SQL DDL scripts are installed to `C:\Program Files\ETL Madness\Flow API\docs\` and `C:\Program Files\ETL Madness\Flow API\share\sql\` (`.msi`) or `%LOCALAPPDATA%\Microsoft\WinGet\Packages\ETLMadness.GoFlowAPI_...\docs\` (`.zip`).
