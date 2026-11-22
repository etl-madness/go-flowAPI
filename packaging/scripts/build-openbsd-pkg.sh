#!/bin/sh
# Builds an OpenBSD (.tgz) package for ETL Madness go-flowAPI.
# Installs binary (go-flowAPI), man pages, GNU Info manual, SQL schema DDL,
# and reference guides under /opt/flow-api, plus OpenBSD rc.d service script
# (/etc/rc.d/go_flow_api), environment sample
# (/opt/flow-api/share/examples/go-flow-api/go-flow-api.env.sample -> /etc/go-flow-api/go-flow-api.env),
# and @sample /etc/profile.d/go-flow-api.sh.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

RAW_VERSION=$(resolve_version)
# OpenBSD packages-specs(7) forbids hyphens inside the version component
# (e.g. 1.3.2-rc1 -> 1.3.2rc1) because hyphens separate package flavors.
OBSD_BASE_VER=$(printf '%s' "$RAW_VERSION" | tr -d '-')
if [ "${FLOW_RELEASE}" -gt 1 ] 2>/dev/null; then
    OBSD_REV=$((FLOW_RELEASE - 1))
    OBSD_PKGVER="${OBSD_BASE_VER}p${OBSD_REV}"
else
    OBSD_PKGVER="${OBSD_BASE_VER}"
fi

GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-}}")

case "$GOARCH_TARGET" in
    amd64) OBSD_ARCH="amd64" ;;
    arm64) OBSD_ARCH="aarch64" ;;
    *)
        log_err "Unsupported OpenBSD architecture: $GOARCH_TARGET (expected amd64 or arm64)"
        exit 1
        ;;
esac

FULLPKGNAME="${PKG_NAME}-${OBSD_PKGVER}"
WORK_DIR="${BUILD_ROOT}/openbsd-${OBSD_ARCH}"
STAGE_DIR="${WORK_DIR}/stage"
META_DIR="${WORK_DIR}/meta"
DOC_TMP="${WORK_DIR}/docs"
PLIST_FILE="${WORK_DIR}/PLIST"

rm -rf "$WORK_DIR"
mkdir -p \
    "${STAGE_DIR}/opt/flow-api/bin" \
    "${STAGE_DIR}/opt/flow-api/share/man/man1" \
    "${STAGE_DIR}/opt/flow-api/share/man/man5" \
    "${STAGE_DIR}/opt/flow-api/share/info" \
    "${STAGE_DIR}/opt/flow-api/share/sql" \
    "${STAGE_DIR}/opt/flow-api/share/doc" \
    "${STAGE_DIR}/opt/flow-api/share/services/openbsd" \
    "${STAGE_DIR}/opt/flow-api/share/services/kerberos" \
    "${STAGE_DIR}/opt/flow-api/share/examples/go-flow-api" \
    "${STAGE_DIR}/opt/flow-api/share/licenses" \
    "${STAGE_DIR}/etc/rc.d" \
    "${STAGE_DIR}/etc/profile.d" \
    "$META_DIR" \
    "$DIST_DIR"

compile_flow_api_binary "openbsd" "$GOARCH_TARGET" "${STAGE_DIR}/opt/flow-api/bin"
stage_compressed_docs "$DOC_TMP"

# OpenBSD @man expects uncompressed roff man pages and manages mandoc.db automatically.
cp "${PACKAGING_DIR}/man/man1/go-flowAPI.1" "${STAGE_DIR}/opt/flow-api/share/man/man1/go-flowAPI.1"
cp "${PACKAGING_DIR}/man/man5/go-flow-api.env.5" "${STAGE_DIR}/opt/flow-api/share/man/man5/go-flow-api.env.5"
cp "${DOC_TMP}/info/go-flow-api.info.gz" "${STAGE_DIR}/opt/flow-api/share/info/go-flow-api.info.gz"
cp "${DOC_TMP}/info/dir" "${STAGE_DIR}/opt/flow-api/share/info/dir"

cp "${REPO_ROOT}/docker/init/sqlite-init.sql" "${STAGE_DIR}/opt/flow-api/share/sql/sqlite-init.sql"
cp "${REPO_ROOT}/docker/init/postgres-init.sql" "${STAGE_DIR}/opt/flow-api/share/sql/postgres-init.sql"
cp "${REPO_ROOT}/docker/init/sqlserver-init.sql" "${STAGE_DIR}/opt/flow-api/share/sql/sqlserver-init.sql"

cp "${REPO_ROOT}/SCHEMA.md" "${STAGE_DIR}/opt/flow-api/share/doc/SCHEMA.md"
cp "${REPO_ROOT}/BOOTSTRAP.md" "${STAGE_DIR}/opt/flow-api/share/doc/BOOTSTRAP.md"
cp "${REPO_ROOT}/docs/API_ENDPOINTS_REFERENCE.md" "${STAGE_DIR}/opt/flow-api/share/doc/API_ENDPOINTS_REFERENCE.md"
cp "${REPO_ROOT}/docs/AD_KERBEROS_SIDECAR_GUIDE.md" "${STAGE_DIR}/opt/flow-api/share/doc/AD_KERBEROS_SIDECAR_GUIDE.md"

cp "${PACKAGING_DIR}/services/openbsd/go_flow_api" "${STAGE_DIR}/opt/flow-api/share/services/openbsd/go_flow_api"
cp "${PACKAGING_DIR}/openbsd/+INSTALL" "${STAGE_DIR}/opt/flow-api/share/services/openbsd/+INSTALL"
cp "${PACKAGING_DIR}/openbsd/+DEINSTALL" "${STAGE_DIR}/opt/flow-api/share/services/openbsd/+DEINSTALL"
cp "${PACKAGING_DIR}/services/kerberos/go-flow-api-krb5-refresh.sh" "${STAGE_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh"
cp "${PACKAGING_DIR}/services/openbsd/go_flow_api" "${STAGE_DIR}/etc/rc.d/go_flow_api"
sed 's|/var/lib/go-flow-api|/var/db/go-flow-api|g' \
    "${PACKAGING_DIR}/etc/go-flow-api/go-flow-api.env" > "${STAGE_DIR}/opt/flow-api/share/examples/go-flow-api/go-flow-api.env.sample"
cp "${PACKAGING_DIR}/etc/profile.d/go-flow-api.sh" "${STAGE_DIR}/opt/flow-api/share/examples/go-flow-api/go-flow-api.sh"
cp "${PACKAGING_DIR}/etc/profile.d/go-flow-api.sh" "${STAGE_DIR}/etc/profile.d/go-flow-api.sh"
cp "${REPO_ROOT}/LICENSE" "${STAGE_DIR}/opt/flow-api/share/licenses/LICENSE"

chmod 0755 \
    "${STAGE_DIR}/opt/flow-api/share/services/openbsd/go_flow_api" \
    "${STAGE_DIR}/opt/flow-api/share/services/openbsd/+INSTALL" \
    "${STAGE_DIR}/opt/flow-api/share/services/openbsd/+DEINSTALL" \
    "${STAGE_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh"

chmod 0555 "${STAGE_DIR}/etc/rc.d/go_flow_api"
chmod 0640 "${STAGE_DIR}/opt/flow-api/share/examples/go-flow-api/go-flow-api.env.sample"

chmod 0644 \
    "${STAGE_DIR}/opt/flow-api/share/man/man1/go-flowAPI.1" \
    "${STAGE_DIR}/opt/flow-api/share/man/man5/go-flow-api.env.5" \
    "${STAGE_DIR}/opt/flow-api/share/info/go-flow-api.info.gz" \
    "${STAGE_DIR}/opt/flow-api/share/info/dir" \
    "${STAGE_DIR}/opt/flow-api/share/sql/"*.sql \
    "${STAGE_DIR}/opt/flow-api/share/doc/"*.md \
    "${STAGE_DIR}/opt/flow-api/share/licenses/LICENSE" \
    "${STAGE_DIR}/opt/flow-api/share/examples/go-flow-api/go-flow-api.sh" \
    "${STAGE_DIR}/etc/profile.d/go-flow-api.sh"

cp "${PACKAGING_DIR}/openbsd/+DESC" "${META_DIR}/+DESC"
sed "s/__FULLPKGNAME__/${FULLPKGNAME}/g" "${PACKAGING_DIR}/openbsd/PLIST.tmpl" > "$PLIST_FILE"

PKG_CREATE_BIN=$(command -v pkg_create 2>/dev/null || true)
if [ -z "$PKG_CREATE_BIN" ] && [ -x /usr/sbin/pkg_create ]; then
    PKG_CREATE_BIN="/usr/sbin/pkg_create"
fi

if [ "$(uname -s)" = "OpenBSD" ] && [ -n "$PKG_CREATE_BIN" ]; then
    # OpenBSD pkg_add(1) validates that the .tgz filename strictly matches
    # <stem>-<version>.tgz, so architecture is separated by directory.
    OUT_DIR="${DIST_DIR}/openbsd-${OBSD_ARCH}"
    mkdir -p "$OUT_DIR"
    OUT_PKG="${OUT_DIR}/${FULLPKGNAME}.tgz"

    log_info "Building native OpenBSD package (${OBSD_ARCH}) via pkg_create..."
    "$PKG_CREATE_BIN" \
        -A "$OBSD_ARCH" \
        -B "$STAGE_DIR" \
        -d "${META_DIR}/+DESC" \
        -D COMMENT="ETL Madness go-flowAPI REST and execution daemon" \
        -D FULLPKGPATH="www/${PKG_NAME}" \
        -D MAINTAINER="${MAINTAINER}" \
        -D HOMEPAGE="${HOMEPAGE}" \
        -D FTP="yes" \
        -f "$PLIST_FILE" \
        -p "/opt/flow-api" \
        "$OUT_PKG"

    if [ -n "${OPENBSD_SIGNIFY_KEY:-}" ]; then
        PKG_SIGN_BIN=$(command -v pkg_sign 2>/dev/null || true)
        [ -z "$PKG_SIGN_BIN" ] && [ -x /usr/sbin/pkg_sign ] && PKG_SIGN_BIN="/usr/sbin/pkg_sign"
        if [ -z "$PKG_SIGN_BIN" ]; then
            log_err "OPENBSD_SIGNIFY_KEY was set, but pkg_sign was not found."
            exit 1
        fi
        SIGNED_DIR="${WORK_DIR}/signed"
        mkdir -p "$SIGNED_DIR"
        log_info "Signing OpenBSD package with signify2 key ${OPENBSD_SIGNIFY_KEY}..."
        "$PKG_SIGN_BIN" -s signify2 -s "$OPENBSD_SIGNIFY_KEY" -o "$SIGNED_DIR" "$OUT_PKG"
        cp "${SIGNED_DIR}/${FULLPKGNAME}.tgz" "$OUT_PKG"
    fi
else
    OUT_PKG="${DIST_DIR}/${PKG_NAME}-${FLOW_VERSION}-openbsd-${OBSD_ARCH}-bundle.tar.gz"
    log_info "OpenBSD pkg_create not present on host; creating cross-compiled root bundle: ${OUT_PKG}"
    tar -czf "$OUT_PKG" -C "$STAGE_DIR" opt etc
fi

log_info "Created OpenBSD package: ${OUT_PKG}"
