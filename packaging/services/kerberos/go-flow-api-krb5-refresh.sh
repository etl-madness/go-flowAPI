#!/bin/sh
# Host-side Active Directory Kerberos TGT & Nested AD Group refresh helper
# for running go-flowAPI outside Docker on Linux, FreeBSD, and macOS.
#
# Performs on bare-metal/VM hosts what docker/krb5-sidecar-entrypoint.sh does in Docker:
#   1. Acquires or renews a MIT FILE: Kerberos TGT (from a keytab, AD_PASSWORD, or existing ccache).
#   2. Verifies the ticket with 'klist -f'.
#   3. Queries Active Directory LDAP over GSSAPI (LDAP_MATCHING_RULE_IN_CHAIN) to write
#      nested DOMAIN\<Group> memberships into FLOW_WINDOWS_GROUPS_FILE (ad-groups.txt).
#
# Usage:
#   # One-shot TGT + AD group sync (ideal for cron, systemd timer, or ExecStartPre):
#   /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --once
#
#   # Continuous background renewal loop (every KRB5_RENEW_INTERVAL_SEC, default 4h):
#   /opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh --loop

set -eu

ENV_FILE="${GO_FLOW_API_ENV_FILE:-/etc/go-flow-api/go-flow-api.env}"
if [ -f "$ENV_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
fi

AD_DOMAIN="${AD_DOMAIN:-EXAMPLE}"
AD_REALM="${AD_REALM:-EXAMPLE.COM}"
AD_USER="${AD_USER:-}"
if [ -z "$AD_USER" ] && [ -n "${FLOW_WINDOWS_USER:-}" ]; then
    AD_USER=$(printf '%s' "$FLOW_WINDOWS_USER" | sed 's|^.*[\\/]||; s|@.*$||')
fi
AD_USER="${AD_USER:-RUN_AS_USER}"
AD_DC_HOST="${AD_DC_HOST:-}"
AD_LDAP_BASE_DN="${AD_LDAP_BASE_DN:-}"

RAW_CCACHE="${KRB5CCNAME:-/var/lib/go-flow-api/krb5cc}"
CCACHE_PATH=$(printf '%s' "$RAW_CCACHE" | sed 's/^[Ff][Ii][Ll][Ee]://')
export KRB5CCNAME="FILE:${CCACHE_PATH}"

KEYTAB_PATH=$(printf '%s' "${KRB5_KTNAME:-/etc/go-flow-api/flowapi.keytab}" | sed 's/^[Ff][Ii][Ll][Ee]://')
GROUPS_FILE="${FLOW_WINDOWS_GROUPS_FILE:-$(dirname "$CCACHE_PATH")/ad-groups.txt}"
PRINCIPAL="${AD_USER}@${AD_REALM}"
MODE="${1:---once}"

mkdir -p "$(dirname "$CCACHE_PATH")" "$(dirname "$GROUPS_FILE")"

refresh_tgt_and_groups() {
    if klist -s 2>/dev/null && kinit -R 2>/dev/null; then
        printf '[krb5-refresh] Renewed existing Kerberos TGT in %s for %s\n' "$KRB5CCNAME" "$PRINCIPAL"
    elif [ -f "$KEYTAB_PATH" ]; then
        printf '[krb5-refresh] Acquiring TGT for %s using keytab %s...\n' "$PRINCIPAL" "$KEYTAB_PATH"
        kinit -kt "$KEYTAB_PATH" "$PRINCIPAL"
    elif [ -n "${AD_PASSWORD:-}" ]; then
        printf '[krb5-refresh] Acquiring TGT for %s using AD_PASSWORD...\n' "$PRINCIPAL"
        printf '%s\n' "$AD_PASSWORD" | kinit "$PRINCIPAL"
    elif [ -f "$CCACHE_PATH" ] && klist -s 2>/dev/null; then
        printf '[krb5-refresh] Using existing valid Kerberos ticket cache %s\n' "$KRB5CCNAME"
    else
        printf '[krb5-refresh] ERROR: No valid TGT in %s and neither keytab (%s) nor AD_PASSWORD available.\n' "$KRB5CCNAME" "$KEYTAB_PATH" >&2
        exit 1
    fi

    chmod 0640 "$CCACHE_PATH" 2>/dev/null || true
    klist -f || true

    if [ -n "${AD_GROUPS:-}" ]; then
        printf '%s\n' "$AD_GROUPS" > "$GROUPS_FILE"
    elif [ ! -f "$GROUPS_FILE" ]; then
        : > "$GROUPS_FILE"
    fi

    if [ -n "$AD_DC_HOST" ] && [ -n "$AD_LDAP_BASE_DN" ] && command -v ldapsearch >/dev/null 2>&1 && klist -s 2>/dev/null; then
        printf '[krb5-refresh] Querying nested AD groups for %s from ldap://%s (%s)...\n' "$AD_USER" "$AD_DC_HOST" "$AD_LDAP_BASE_DN"
        USER_DN=$(ldapsearch -LLL -Y GSSAPI -H "ldap://${AD_DC_HOST}" -b "$AD_LDAP_BASE_DN" "(sAMAccountName=${AD_USER})" dn 2>/dev/null | sed -n 's/^dn: //p' | head -n 1)
        if [ -n "$USER_DN" ]; then
            LDAP_GROUPS=$(ldapsearch -LLL -Y GSSAPI -H "ldap://${AD_DC_HOST}" -b "$AD_LDAP_BASE_DN" \
                "(member:1.2.840.113556.1.4.1941:=${USER_DN})" sAMAccountName 2>/dev/null \
                | sed -n 's/^sAMAccountName: //p' \
                | awk -v dom="$AD_DOMAIN" '{print dom "\\" $0}')
            if [ -n "$LDAP_GROUPS" ]; then
                printf '%s\n' "$LDAP_GROUPS" > "$GROUPS_FILE"
                printf '[krb5-refresh] Synced AD groups to %s\n' "$GROUPS_FILE"
            fi
        fi
    fi
    chmod 0640 "$GROUPS_FILE" 2>/dev/null || true
}

refresh_tgt_and_groups

if [ "$MODE" = "--loop" ]; then
    INTERVAL="${KRB5_RENEW_INTERVAL_SEC:-14400}"
    while true; do
        sleep "$INTERVAL"
        refresh_tgt_and_groups
    done
fi
