# SQL Server Kerberos connection fix

## Goal

Connect the API's `FLOW_API_DSN` database using the Kerberos identity `EXAMPLE\SVC_RUNAS_USER`, rather than a SQL Server login. The Linux containers do not automatically inherit the Windows host's login.

## What was wrong

- The private Compose DSN contained HTML-escaped separators (`&amp;`). Those are not valid substitutes for `&` in a Compose connection string, so the SQL Server driver did not receive the intended authentication and connection options.
- The private Compose file mapped the domain controller hostname to Docker's host gateway. That gateway was not the KDC, and a service-ticket request failed because port 88 refused the connection.
- The sidecar health check could pass without a valid ticket when `AD_PASSWORD` was empty. A running sidecar therefore did not necessarily mean Kerberos was ready.
- The running API's health response had an empty `db_user`. The API being up did not establish that its database connection had succeeded.

## Changes made

1. In the local, git-ignored `private-docker-compose.ad-sidecar.yml`, replaced the escaped DSN separators with literal `&` and specified `integrated+security=true` and `authenticator=krb5`. The explicit authenticator makes Kerberos provider errors visible instead of allowing the driver's default provider to fall back to SQL authentication.
2. Removed the domain controller's `host-gateway` mappings from both private Compose services so the controller resolves through DNS. The SQL Server host mapping was retained.
3. Changed the sidecar health check to require `klist -s`, in addition to the identity file, before Compose starts the API.
4. Made the corresponding changes in the reusable [example Compose file](../docker-compose.ad-sidecar.yml), including an integrated-authentication default DSN.
5. Updated the [Kerberos sidecar guide](./AD_KERBEROS_SIDECAR_GUIDE.md) and Docker guides with configuration, verification, and troubleshooting instructions.

The existing local `docker/private-krb5.conf` was not changed. Its configured KDC must remain reachable from the containers. A valid ticket must be acquired using a password or keytab, or supplied in a pre-seeded cache; `FLOW_WINDOWS_USER` and `AD_GROUPS` do not themselves authenticate the database connection.

## Verification performed

The existing sidecar and API containers both had a valid ticket for `SVC_RUNAS_USER@EXAMPLE.COM`. An isolated Linux container then used the corrected DSN, the existing ticket cache, and DNS for the domain controller to query SQL Server. It returned:

```text
SYSTEM_USER=EXAMPLE\SVC_RUNAS_USER
ORIGINAL_LOGIN=EXAMPLE\SVC_RUNAS_USER
DB_NAME=PROTO
APP_NAME=goflow-api-connector
```

Both Compose configurations passed `docker compose config --quiet`, and `go test ./...` passed. This probe did not restart or replace the existing application containers; applying the Compose changes requires the command below.

## Apply and check

From the repository root, provide the sidecar credentials or a keytab as described in the [sidecar guide](./AD_KERBEROS_SIDECAR_GUIDE.md), then run:

```powershell
docker compose -f private-docker-compose.ad-sidecar.yml up -d --build
docker compose -f private-docker-compose.ad-sidecar.yml ps
docker exec go-flow-api klist -f
Invoke-RestMethod http://localhost:5180/api/v1/health |
  Select-Object status, db_user, application_name
```

Confirm that the ticket principal is `SVC_RUNAS_USER@EXAMPLE.COM` and `db_user` is `EXAMPLE\SVC_RUNAS_USER`. A host `FLOW_API_DSN` environment variable overrides the Compose default; if it is set, it must contain the same intended Kerberos options. The API health `status` alone does not confirm database connectivity.
