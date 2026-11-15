#!/bin/sh
# Builds a FreeBSD (.pkg) package for ETL Madness go-flowAPI.
# Installs binary (go-flowAPI), man pages, GNU Info manual, SQL schema DDL,
# and reference guides under /opt/flow-api, plus FreeBSD rc.d service script
# (/usr/local/etc/rc.d/go_flow_api), environment sample
# (/usr/local/etc/go-flow-api/go-flow-api.env.sample), and PATH/MANPATH drop-ins.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

if ! command -v pkg >/dev/null 2>&1; then
    log_err "FreeBSD 'pkg' utility is required (run this script on FreeBSD)."
    exit 1
fi

FLOW_VERSION=$(resolve_version)
GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-}}")
FREEBSD_MAJOR="${FREEBSD_MAJOR:-14}"

case "$GOARCH_TARGET" in
    amd64) FBSD_ARCH="amd64" ;;
    arm64) FBSD_ARCH="aarch64" ;;
    *)
        log_err "Unsupported FreeBSD architecture: $GOARCH_TARGET"
        exit 1
        ;;
esac

FBSD_ABI="FreeBSD:${FREEBSD_MAJOR}:${FBSD_ARCH}"
WORK_DIR="${BUILD_ROOT}/freebsd-${FBSD_ARCH}"
STAGE_DIR="${WORK_DIR}/stage"
META_DIR="${WORK_DIR}/meta"
DOC_TMP="${WORK_DIR}/docs"
PLIST_FILE="${WORK_DIR}/pkg-plist"

rm -rf "$WORK_DIR"
mkdir -p \
    "${STAGE_DIR}/opt/flow-api/bin" \
    "${STAGE_DIR}/opt/flow-api/share/man/man1" \
    "${STAGE_DIR}/opt/flow-api/share/man/man5" \
    "${STAGE_DIR}/opt/flow-api/share/info" \
    "${STAGE_DIR}/opt/flow-api/share/sql" \
    "${STAGE_DIR}/opt/flow-api/share/doc" \
    "${STAGE_DIR}/opt/flow-api/share/services/freebsd" \
    "${STAGE_DIR}/opt/flow-api/share/services/kerberos" \
    "${STAGE_DIR}/opt/flow-api/share/licenses" \
    "${STAGE_DIR}/usr/local/etc/rc.d" \
    "${STAGE_DIR}/usr/local/etc/go-flow-api" \
    "${STAGE_DIR}/usr/local/etc/profile.d" \
    "${STAGE_DIR}/usr/local/etc/man.d" \
    "$META_DIR" \
    "$DIST_DIR"

compile_flow_api_binary "freebsd" "$GOARCH_TARGET" "${STAGE_DIR}/opt/flow-api/bin"
stage_compressed_docs "$DOC_TMP"

cp "${DOC_TMP}/man1/"*.1.gz "${STAGE_DIR}/opt/flow-api/share/man/man1/"
cp "${DOC_TMP}/man5/"*.5.gz "${STAGE_DIR}/opt/flow-api/share/man/man5/"
cp "${DOC_TMP}/info/go-flow-api.info.gz" "${STAGE_DIR}/opt/flow-api/share/info/go-flow-api.info.gz"
cp "${DOC_TMP}/info/dir" "${STAGE_DIR}/opt/flow-api/share/info/dir"

cp "${REPO_ROOT}/docker/init/sqlite-init.sql" "${STAGE_DIR}/opt/flow-api/share/sql/sqlite-init.sql"
cp "${REPO_ROOT}/docker/init/postgres-init.sql" "${STAGE_DIR}/opt/flow-api/share/sql/postgres-init.sql"
cp "${REPO_ROOT}/docker/init/sqlserver-init.sql" "${STAGE_DIR}/opt/flow-api/share/sql/sqlserver-init.sql"

cp "${REPO_ROOT}/SCHEMA.md" "${STAGE_DIR}/opt/flow-api/share/doc/SCHEMA.md"
cp "${REPO_ROOT}/BOOTSTRAP.md" "${STAGE_DIR}/opt/flow-api/share/doc/BOOTSTRAP.md"
cp "${REPO_ROOT}/docs/API_ENDPOINTS_REFERENCE.md" "${STAGE_DIR}/opt/flow-api/share/doc/API_ENDPOINTS_REFERENCE.md"
cp "${REPO_ROOT}/docs/AD_KERBEROS_SIDECAR_GUIDE.md" "${STAGE_DIR}/opt/flow-api/share/doc/AD_KERBEROS_SIDECAR_GUIDE.md"

cp "${PACKAGING_DIR}/services/freebsd/go_flow_api" "${STAGE_DIR}/opt/flow-api/share/services/freebsd/go_flow_api"
cp "${PACKAGING_DIR}/services/kerberos/go-flow-api-krb5-refresh.sh" "${STAGE_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh"
cp "${PACKAGING_DIR}/services/freebsd/go_flow_api" "${STAGE_DIR}/usr/local/etc/rc.d/go_flow_api"
sed 's|/var/lib/go-flow-api|/var/db/go-flow-api|g' \
    "${PACKAGING_DIR}/etc/go-flow-api/go-flow-api.env" > "${STAGE_DIR}/usr/local/etc/go-flow-api/go-flow-api.env.sample"

cp "${REPO_ROOT}/LICENSE" "${STAGE_DIR}/opt/flow-api/share/licenses/LICENSE"
cp "${PACKAGING_DIR}/etc/profile.d/go-flow-api.sh" "${STAGE_DIR}/usr/local/etc/profile.d/go-flow-api.sh"
printf 'MANPATH /opt/flow-api/share/man\n' > "${STAGE_DIR}/usr/local/etc/man.d/go-flow-api.conf"

chmod 0755 \
    "${STAGE_DIR}/opt/flow-api/share/services/freebsd/go_flow_api" \
    "${STAGE_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh" \
    "${STAGE_DIR}/usr/local/etc/rc.d/go_flow_api"

chmod 0640 "${STAGE_DIR}/usr/local/etc/go-flow-api/go-flow-api.env.sample"

chmod 0644 \
    "${STAGE_DIR}/opt/flow-api/share/man/man1/"*.1.gz \
    "${STAGE_DIR}/opt/flow-api/share/man/man5/"*.5.gz \
    "${STAGE_DIR}/opt/flow-api/share/info/go-flow-api.info.gz" \
    "${STAGE_DIR}/opt/flow-api/share/info/dir" \
    "${STAGE_DIR}/opt/flow-api/share/sql/"*.sql \
    "${STAGE_DIR}/opt/flow-api/share/doc/"*.md \
    "${STAGE_DIR}/opt/flow-api/share/licenses/LICENSE" \
    "${STAGE_DIR}/usr/local/etc/profile.d/go-flow-api.sh" \
    "${STAGE_DIR}/usr/local/etc/man.d/go-flow-api.conf"

SIZE_KB=$(du -sk "${STAGE_DIR}/opt/flow-api" "${STAGE_DIR}/usr/local/etc" | awk '{s+=$1} END {print s}')
FLATSIZE=$((SIZE_KB * 1024))

sed \
    -e "s/__VERSION__/${FLOW_VERSION}/g" \
    -e "s/__ABI__/${FBSD_ABI}/g" \
    -e "s/__FLATSIZE__/${FLATSIZE}/g" \
    "${PACKAGING_DIR}/freebsd/+MANIFEST.tmpl" > "${META_DIR}/+MANIFEST"

cp "${PACKAGING_DIR}/freebsd/+POST_INSTALL" "${META_DIR}/+POST_INSTALL"
cp "${PACKAGING_DIR}/freebsd/+PRE_DEINSTALL" "${META_DIR}/+PRE_DEINSTALL"
chmod 0755 "${META_DIR}/+POST_INSTALL" "${META_DIR}/+PRE_DEINSTALL"

cat > "$PLIST_FILE" <<'EOF'
/opt/flow-api/bin/go-flowAPI
/opt/flow-api/share/man/man1/go-flowAPI.1.gz
/opt/flow-api/share/man/man5/go-flow-api.env.5.gz
/opt/flow-api/share/info/go-flow-api.info.gz
/opt/flow-api/share/info/dir
/opt/flow-api/share/sql/sqlite-init.sql
/opt/flow-api/share/sql/postgres-init.sql
/opt/flow-api/share/sql/sqlserver-init.sql
/opt/flow-api/share/doc/SCHEMA.md
/opt/flow-api/share/doc/BOOTSTRAP.md
/opt/flow-api/share/doc/API_ENDPOINTS_REFERENCE.md
/opt/flow-api/share/doc/AD_KERBEROS_SIDECAR_GUIDE.md
/opt/flow-api/share/services/freebsd/go_flow_api
/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh
/opt/flow-api/share/licenses/LICENSE
/usr/local/etc/rc.d/go_flow_api
/usr/local/etc/go-flow-api/go-flow-api.env.sample
/usr/local/etc/profile.d/go-flow-api.sh
/usr/local/etc/man.d/go-flow-api.conf
EOF

PKG_TMP_OUT="${WORK_DIR}/pkg-out"
mkdir -p "$PKG_TMP_OUT"

log_info "Building FreeBSD package (${FBSD_ABI})..."
pkg create \
    -m "$META_DIR" \
    -p "$PLIST_FILE" \
    -r "$STAGE_DIR" \
    -o "$PKG_TMP_OUT"

BUILT_PKG=$(find "$PKG_TMP_OUT" -name '*.pkg' -o -name '*.txz' -o -name '*.tzst' | head -n 1)
if [ -z "$BUILT_PKG" ]; then
    log_err "pkg create completed but no package archive was found in ${PKG_TMP_OUT}"
    exit 1
fi

OUT_PKG="${DIST_DIR}/${PKG_NAME}-${FLOW_VERSION}-freebsd-${FBSD_ARCH}.pkg"
cp "$BUILT_PKG" "$OUT_PKG"

log_info "Created FreeBSD package: ${OUT_PKG}"
