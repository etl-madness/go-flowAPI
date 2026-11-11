#!/bin/sh
set -e

AD_DOMAIN="${AD_DOMAIN:-EXAMPLE}"
AD_REALM="${AD_REALM:-EXAMPLE.COM}"
AD_USER="${AD_USER:-RUN_AS_USER}"
AD_DC_HOST="${AD_DC_HOST:-dc.example.com}"
AD_LDAP_BASE_DN="${AD_LDAP_BASE_DN:-DC=example,DC=com}"
KRB5CCNAME="${KRB5CCNAME:-/krb5cache/krb5cc_1000}"
export KRB5CCNAME

mkdir -p /krb5cache
chmod 1777 /krb5cache

PRINCIPAL="${AD_USER}@${AD_REALM}"
echo "[krb5-sidecar] Starting Active Directory Kerberos sidecar for ${AD_DOMAIN}\\${AD_USER} (${PRINCIPAL})"

acquire_tgt_and_groups() {
    if [ -f /etc/krb5.keytab ]; then
        echo "[krb5-sidecar] Acquiring TGT using keytab /etc/krb5.keytab..."
        kinit -kt /etc/krb5.keytab "$PRINCIPAL"
    elif [ -n "$AD_PASSWORD" ]; then
        echo "[krb5-sidecar] Acquiring TGT using AD_PASSWORD..."
        printf '%s\n' "$AD_PASSWORD" | kinit "$PRINCIPAL"
    elif [ -f "/krb5cache/krb5cc_1000" ] && klist -s; then
        echo "[krb5-sidecar] Using existing valid Kerberos ticket cache in $KRB5CCNAME"
    else
        echo "[krb5-sidecar] WARNING: Neither /etc/krb5.keytab nor AD_PASSWORD provided, and no pre-seeded ticket cache found."
    fi

    if [ -f "/krb5cache/krb5cc_1000" ]; then
        chmod 644 /krb5cache/krb5cc_1000
    fi

    echo "[krb5-sidecar] Active Kerberos Ticket Cache (klist -f):"
    klist -f || true

    # Write resolved identity env file
    printf 'FLOW_WINDOWS_USER=%s\\%s\nHOST_USERDOMAIN=%s\nHOST_USERNAME=%s\n' \
        "$AD_DOMAIN" "$AD_USER" "$AD_DOMAIN" "$AD_USER" > /krb5cache/ad-identity.env

    # Seed initial groups from AD_GROUPS env var if provided
    if [ -n "$AD_GROUPS" ]; then
        printf '%s\n' "$AD_GROUPS" > /krb5cache/ad-groups.txt
    elif [ ! -f /krb5cache/ad-groups.txt ]; then
        : > /krb5cache/ad-groups.txt
    fi

    # Optionally query Active Directory LDAP via GSSAPI (Kerberos) for nested group memberships (LDAP_MATCHING_RULE_IN_CHAIN)
    if command -v ldapsearch >/dev/null 2>&1 && klist -s 2>/dev/null; then
        echo "[krb5-sidecar] Querying AD groups for ${AD_USER} from ldap://${AD_DC_HOST} (${AD_LDAP_BASE_DN})..."
        USER_DN=$(ldapsearch -LLL -Y GSSAPI -H "ldap://${AD_DC_HOST}" -b "$AD_LDAP_BASE_DN" "(sAMAccountName=${AD_USER})" dn 2>/dev/null | sed -n 's/^dn: //p' | head -n 1)
        if [ -n "$USER_DN" ]; then
            LDAP_GROUPS=$(ldapsearch -LLL -Y GSSAPI -H "ldap://${AD_DC_HOST}" -b "$AD_LDAP_BASE_DN" \
                "(member:1.2.840.113556.1.4.1941:=${USER_DN})" sAMAccountName 2>/dev/null \
                | sed -n 's/^sAMAccountName: //p' \
                | awk -v dom="$AD_DOMAIN" '{print dom "\\" $0}')
            if [ -n "$LDAP_GROUPS" ]; then
                printf '%s\n' "$LDAP_GROUPS" > /krb5cache/ad-groups.txt
                echo "[krb5-sidecar] Resolved AD groups for ${AD_DOMAIN}\\${AD_USER}:"
                cat /krb5cache/ad-groups.txt
            fi
        fi
    fi

    chmod 644 /krb5cache/ad-identity.env /krb5cache/ad-groups.txt 2>/dev/null || true
}

acquire_tgt_and_groups

RENEW_INTERVAL="${KRB5_RENEW_INTERVAL_SEC:-14400}"
while true; do
    sleep "$RENEW_INTERVAL"
    echo "[krb5-sidecar] Refreshing Kerberos TGT for $PRINCIPAL..."
    if ! kinit -R 2>/dev/null; then
        acquire_tgt_and_groups
    else
        klist -f || true
    fi
done
