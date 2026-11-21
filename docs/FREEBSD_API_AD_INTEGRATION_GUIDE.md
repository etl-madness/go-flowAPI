# Running `go-flowAPI` on FreeBSD with Kerberos (`kinit SVC_FLOW_API@EXAMPLE.COM`)

This guide explains how to run **`go-flowAPI`** natively on FreeBSD using an Active Directory Kerberos ticket (`kinit SVC_FLOW_API@EXAMPLE.COM`), both interactively in a shell and as the FreeBSD `rc.d` background service (`go_flow_api`).

Testing was performed on FreeBSD 15.x with MIT Kerberos and OpenLDAP GSSAPI client installed via `pkg`.

**Note:** Replace the placeholders below with your actual environment values:
 - Replace `EXAMPLE` and `SVC_FLOW_API` with your actual Active Directory domain and service account as needed. 
 - Replace `DB.EXAMPLE.COM` and `EXAMPLEDB` with your actual SQL Server host and database name.
 - Replace `DC1.EXAMPLE.com` with your actual Active Directory domain controller hostname.

---

## 1. Install MIT Kerberos & OpenLDAP GSSAPI Client on FreeBSD

FreeBSD includes Heimdal Kerberos in the base system (`/usr/bin/kinit`), but installing MIT `krb5` and OpenLDAP SASL/GSSAPI from `pkg` ensures full compatibility with Active Directory and dynamic nested AD group resolution (`ldapsearch -Y GSSAPI`):

```sh
sudo pkg install krb5 openldap26-client cyrus-sasl-gssapi
```

> **Note:** `pkg` installs the MIT Kerberos tools at `/usr/local/bin/kinit` and `/usr/local/bin/klist`. Use `/usr/local/bin/kinit` and `/usr/local/bin/klist` explicitly (or place `/usr/local/bin` before `/usr/bin` in `PATH`) so you don't accidentally invoke base-system Heimdal `/usr/bin/kinit`.

---

## 2. Configure `/etc/krb5.conf` for `EXAMPLE.COM`

Create or update `/etc/krb5.conf` (matching `docker/private-krb5.conf`). Ensure your FreeBSD host can resolve and reach `DC1.EXAMPLE.com` on TCP/UDP port `88` (Kerberos) and `389` (LDAP), and `DB.EXAMPLE.COM` on TCP port `1433` (SQL Server):

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
        kdc = DC1.EXAMPLE.com
        admin_server = DC1.EXAMPLE.com
        default_domain = EXAMPLE.com
    }
    EXAMPLE = {
        kdc = DC1.EXAMPLE.com
        admin_server = DC1.EXAMPLE.com
    }

[domain_realm]
    .EXAMPLE.com = EXAMPLE.COM
    EXAMPLE.com = EXAMPLE.COM
    dc1 = EXAMPLE.COM
    dc1.EXAMPLE.com = EXAMPLE.COM
    DB = EXAMPLE.COM
    DB.EXAMPLE.com = EXAMPLE.COM
    DB = EXAMPLE.COM
    DB.EXAMPLE.COM = EXAMPLE.COM
```

---

## 3. Option A: Interactive Execution in a FreeBSD Shell (`kinit SVC_FLOW_API@EXAMPLE.COM`)

1. **Acquire a `FILE:` Kerberos ticket cache and verify it:**
   *(Pure-Go `gokrb5` requires a standard `FILE:` credential cache; `db.go` automatically strips any `FILE:` prefix when opening the file).*

   ```sh
   export KRB5CCNAME="FILE:/tmp/krb5cc_$(id -u)"
   export KRB5_CONFIG="/etc/krb5.conf"
   export AD_REALM="EXAMPLE.COM"

   /usr/local/bin/kinit SVC_FLOW_API@EXAMPLE.COM
   /usr/local/bin/klist -f
   ```

2. **(Optional) Sync nested Active Directory groups for `EXAMPLE\SVC_FLOW_API`:**
   Use the bundled `go-flow-api-krb5-refresh.sh` helper (or set `FLOW_WINDOWS_GROUPS` manually):

   ```sh
   AD_DOMAIN="EXAMPLE" \
   AD_REALM="EXAMPLE.COM" \
   AD_USER="SVC_FLOW_API" \
   AD_DC_HOST="dc1.EXAMPLE.com" \
   AD_LDAP_BASE_DN="DC=EXAMPLE,DC=com" \
   KRB5CCNAME="$KRB5CCNAME" \
   FLOW_WINDOWS_GROUPS_FILE="/tmp/ad-groups-$(id -u).txt" \
     /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once
   ```

3. **Start `go-flowAPI` with Kerberos Integrated SQL Server Authentication:**
   > **Important:**
   > - Always export `KRB5CCNAME` explicitly (`export KRB5CCNAME="/tmp/krb5cc_$(id -u)"`). Even though `klist -f` automatically finds `/tmp/krb5cc_<uid>` when `$KRB5CCNAME` is unset, `go-mssqldb` only reads `os.Getenv("KRB5CCNAME")` and will not guess `/tmp/krb5cc_<uid>`.
   > - Run `unset KRB5_KTNAME` when authenticating from a ticket cache (`KRB5CCNAME`), because `go-mssqldb` gives `KRB5_KTNAME` precedence over `KRB5CCNAME` if both are set.
   > - Use a **single backslash** inside single quotes (`'EXAMPLE\SVC_FLOW_API'`) so POSIX `sh` does not pass a literal double backslash (`EXAMPLE\\SVC_FLOW_API`).

   ```sh
   unset KRB5_KTNAME
   export KRB5CCNAME="/tmp/krb5cc_$(id -u)"
   export KRB5_CONFIG="/etc/krb5.conf"
   export AD_REALM="EXAMPLE.COM"

   export FLOW_WINDOWS_USER='EXAMPLE\SVC_FLOW_API'
   export FLOW_WINDOWS_GROUPS_FILE="/tmp/ad-groups-$(id -u).txt"
   export HOST_USERDOMAIN="EXAMPLE"
   export HOST_USERNAME="SVC_FLOW_API"
   export FLOW_API_DSN="sqlserver://DB.EXAMPLE.COM:1433?database=EXAMPLEDB&integrated+security=true&authenticator=krb5&trustServerCertificate=true&app+name=goflow-api-connector"

   /opt/flow-api/bin/go-flowAPI -addr :5180
   ```

---

## 4. Option B: FreeBSD `rc.d` Background Service (`go_flow_api`)

When installed via the FreeBSD package (`sudo pkg add ./packaging/dist/go-flow-api-*-freebsd-amd64.pkg`), the service script `/usr/local/etc/rc.d/go_flow_api` runs as the unprivileged `flowapi` user and reads `/usr/local/etc/go-flow-api/go-flow-api.env`.

### Step 1: Configure `/usr/local/etc/go-flow-api/go-flow-api.env`

```dotenv
FLOW_API_ADDR=":5180"
FLOW_API_DSN="sqlserver://DB.EXAMPLE.COM:1433?database=EXAMPLEDB&integrated+security=true&authenticator=krb5&trustServerCertificate=true&app+name=goflow-api-connector"

AD_DOMAIN="EXAMPLE"
AD_REALM="EXAMPLE.COM"
AD_USER="SVC_FLOW_API"
AD_DC_HOST="dc1.EXAMPLE.com"
AD_LDAP_BASE_DN="DC=EXAMPLE,DC=com"

KRB5_CONFIG="/etc/krb5.conf"
KRB5CCNAME="/var/db/go-flow-api/krb5cc"
FLOW_WINDOWS_USER="EXAMPLE\SVC_FLOW_API"
FLOW_WINDOWS_GROUPS_FILE="/var/db/go-flow-api/ad-groups.txt"
HOST_USERDOMAIN="EXAMPLE"
HOST_USERNAME="SVC_FLOW_API"
```

> **Important `go-flow-api.env` Rules (`loadEnvFile` in `config.go`):**
> - **Keep `FLOW_API_DSN` on a single line** (do not wrap long lines across multiple lines).
> - **Do not use shell expansions like `$(id -u)`** inside `go-flow-api.env` — the file is parsed directly by Go (`config.go`), not `/bin/sh`.
> - **Do not define duplicate keys** (e.g., two `KRB5CCNAME=` lines) — `loadEnvFile` only sets a variable if it is currently empty, so the **first** occurrence in the file wins and later duplicates are ignored.
> - **Omit `KRB5_KTNAME`** from `go-flow-api.env` when using `KRB5CCNAME`, as `go-mssqldb` gives `KRB5_KTNAME` precedence over `KRB5CCNAME`.

Lock down permissions on the environment file:

```sh
sudo chown root:flowapi /usr/local/etc/go-flow-api/go-flow-api.env
sudo chmod 0640 /usr/local/etc/go-flow-api/go-flow-api.env
```

### Step 2: Acquire Initial Ticket Cache (`/var/db/go-flow-api/krb5cc`) as `flowapi`

- **Copy an existing ticket from `/tmp/krb5cc_<uid>` or run interactive `kinit` as `flowapi`:**

  ```sh
  # If you already acquired a ticket in /tmp/krb5cc_$(id -u), copy it for flowapi:
  sudo install -o flowapi -g flowapi -m 0600 "/tmp/krb5cc_$(id -u)" /var/db/go-flow-api/krb5cc

  # Or acquire directly as flowapi:
  sudo -u flowapi KRB5CCNAME="FILE:/var/db/go-flow-api/krb5cc" /usr/local/bin/kinit SVC_FLOW_API@EXAMPLE.COM

  # Sync nested AD groups:
  sudo -u flowapi PATH="/usr/local/bin:$PATH" GO_FLOW_API_ENV_FILE=/usr/local/etc/go-flow-api/go-flow-api.env \
    /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once
  ```

- **Or Unattended via Keytab:**
  A keytab file stores a pre-derived Kerberos key for `SVC_FLOW_API@EXAMPLE.COM` so `kinit -kt` can acquire tickets without prompting for a password. You can create it in one of two ways:

  1. **Directly on FreeBSD using MIT `ktutil` (run as your normal FreeBSD user or `root`; no AD Domain Admin rights needed):**
     *(Always include `-f` so `ktutil` fetches the exact case-sensitive AES salt from the Active Directory KDC, or add a second `rc4-hmac` entry if AES256 is not enabled on the AD account):*

     ```sh
     /usr/local/bin/ktutil
     ```

     Inside the `ktutil:` prompt, run:

     ```text
     addent -password -p SVC_FLOW_API@EXAMPLE.COM -k 1 -f
     (enter password for SVC_FLOW_API@EXAMPLE.COM)
     addent -password -p SVC_FLOW_API@EXAMPLE.COM -k 1 -e rc4-hmac
     (enter password for SVC_FLOW_API@EXAMPLE.COM)
     wkt ./EXAMPLE.keytab
     quit
     ```

  2. **Or on a Windows AD machine using `ktpass`:**

     ```powershell
     ktpass /out EXAMPLE.keytab /princ SVC_FLOW_API@EXAMPLE.COM /pass "YourADPassword" /crypto AES256-SHA1 /ptype KRB5_NT_PRINCIPAL
     ```

  Once created, install the keytab with `0640` permissions for the `flowapi` user:

  ```sh
  sudo install -o root -g flowapi -m 0640 ./EXAMPLE.keytab /usr/local/etc/go-flow-api/flowapi.keytab
  rm -f ./EXAMPLE.keytab
  ```

  Test acquiring the ticket cache and syncing AD groups with the keytab:

  ```sh
  sudo -u flowapi PATH="/usr/local/bin:$PATH" \
    KRB5_KTNAME="/usr/local/etc/go-flow-api/flowapi.keytab" \
    GO_FLOW_API_ENV_FILE=/usr/local/etc/go-flow-api/go-flow-api.env \
    /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once
  ```

  *(Note: If you set `KRB5_KTNAME` inside `go-flow-api.env`, `go-mssqldb` will authenticate directly against the keytab instead of `KRB5CCNAME`. Leaving `KRB5_KTNAME` out of `go-flow-api.env` and passing it only to `go-flow-api-krb5-refresh.sh` ensures `go-flowAPI` always uses the verified `KRB5CCNAME=/var/db/go-flow-api/krb5cc` ticket cache.)*

### Step 3: Schedule Automatic 4-Hour Ticket Renewal on FreeBSD (`cron`)

FreeBSD `cron(8)` scans `/etc/cron.d` by default, while `/usr/local/etc/cron.d` is not pre-created by the base OS unless a port creates it. You can either place the drop-in file in `/etc/cron.d/go-flow-api-krb5` (or create `/usr/local/etc/cron.d` first):

```sh
# Option 1: Use FreeBSD's base /etc/cron.d directory (exists out of the box)
echo 'PATH=/usr/local/bin:/usr/bin:/bin
0 */4 * * * flowapi GO_FLOW_API_ENV_FILE=/usr/local/etc/go-flow-api/go-flow-api.env /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once >/var/log/go-flow-api/krb5-refresh.log 2>&1' \
  | sudo tee /etc/cron.d/go-flow-api-krb5 >/dev/null
sudo chmod 0644 /etc/cron.d/go-flow-api-krb5
```

*(Alternatively, if you prefer `/usr/local/etc/cron.d`, run `sudo mkdir -p /usr/local/etc/cron.d` first, or install a user crontab for `flowapi` via `sudo crontab -u flowapi -e`.)*

### Step 4: Enable and Start `go_flow_api`

```sh
sudo sysrc go_flow_api_enable="YES"
sudo service go_flow_api start
sudo service go_flow_api status
```

### Step 5: Verify Health & Kerberos SQL Server Identity

```sh
# Check active ticket cache for the flowapi service user
sudo -u flowapi KRB5CCNAME="FILE:/var/db/go-flow-api/krb5cc" /usr/local/bin/klist -f

# Verify the API health endpoint reports db_user: "EXAMPLE\\SVC_FLOW_API"
curl -s http://localhost:5180/api/v1/health
```

---

## 5. Troubleshooting Common FreeBSD Kerberos Issues

1. **`klist -f` shows a valid ticket (`/tmp/krb5cc_1001`), but `go-flowAPI` will not connect to SQL Server:**
   - **`KRB5CCNAME` not exported:** MIT `kinit`/`klist` defaults to `/tmp/krb5cc_<uid>` even when `$KRB5CCNAME` is unset in the shell, but `go-mssqldb` requires `KRB5CCNAME` to be explicitly exported (`export KRB5CCNAME="/tmp/krb5cc_$(id -u)"`).
   - **`KRB5_KTNAME` still set in the environment:** `go-mssqldb` prioritizes `KRB5_KTNAME` over `KRB5CCNAME`. Run `unset KRB5_KTNAME` when testing with a ticket cache.
   - **Double backslash in single quotes:** Ensure `FLOW_WINDOWS_USER='EXAMPLE\SVC_FLOW_API'` uses a single backslash inside single quotes, and `HOST_USERNAME="SVC_FLOW_API"` matches the AD username (not the local Unix `flowapi` user).

2. **`kinit: Preauthentication failed while getting initial credentials` when using a keytab:**
   - Active Directory AES256 salts are **case-sensitive** based on the account's exact `sAMAccountName` in AD, and some AD accounts do not have *"This account supports Kerberos AES 256 bit encryption"* enabled.
   - Run `/usr/local/bin/kinit SVC_FLOW_API@EXAMPLE.COM` and `/usr/local/bin/klist` interactively first to confirm the exact principal case returned by the KDC, then recreate the keytab using `addent -password -p SVC_FLOW_API@EXAMPLE.COM -k 1 -f` (so `ktutil` fetches the exact salt from the KDC) plus `addent -password -p SVC_FLOW_API@EXAMPLE.COM -k 1 -e rc4-hmac`.

3. **Verify the SQL Server SPN (`MSSQLSvc`) directly from FreeBSD:**

   ```sh
   /usr/local/bin/kvno MSSQLSvc/DB.EXAMPLE.COM:1433
   /usr/local/bin/klist -ef
   ```
