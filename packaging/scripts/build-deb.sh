#!/bin/sh
# Builds a Debian/Ubuntu .deb package for ETL Madness go-flowAPI.
# Installs binary (go-flowAPI), man pages, GNU Info manual, SQL schema DDL,
# reference guides, and systemd/OpenRC/SysVinit service definitions under /opt/flow-api,
# plus /usr/lib/systemd/system/go-flow-api.service, /etc/go-flow-api/go-flow-api.env,
# and /etc/profile.d/go-flow-api.sh.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

FLOW_VERSION=$(resolve_version)
GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-}}")

case "$GOARCH_TARGET" in
    amd64) DEB_ARCH="amd64" ;;
    arm64) DEB_ARCH="arm64" ;;
    *)
        log_err "Unsupported .deb architecture: $GOARCH_TARGET"
        exit 1
        ;;
esac

WORK_DIR="${BUILD_ROOT}/deb-${DEB_ARCH}"
STAGE_DIR="${WORK_DIR}/root"
DOC_TMP="${WORK_DIR}/docs"
OUT_DEB="${DIST_DIR}/${PKG_NAME}_${FLOW_VERSION}-${FLOW_RELEASE}_${DEB_ARCH}.deb"

rm -rf "$WORK_DIR"
mkdir -p \
    "${STAGE_DIR}/DEBIAN" \
    "${STAGE_DIR}/opt/flow-api/bin" \
    "${STAGE_DIR}/opt/flow-api/share/man/man1" \
    "${STAGE_DIR}/opt/flow-api/share/man/man5" \
    "${STAGE_DIR}/opt/flow-api/share/info" \
    "${STAGE_DIR}/opt/flow-api/share/sql" \
    "${STAGE_DIR}/opt/flow-api/share/doc" \
    "${STAGE_DIR}/opt/flow-api/share/services/systemd" \
    "${STAGE_DIR}/opt/flow-api/share/services/openrc" \
    "${STAGE_DIR}/opt/flow-api/share/services/sysvinit" \
    "${STAGE_DIR}/opt/flow-api/share/services/kerberos" \
    "${STAGE_DIR}/opt/flow-api/share/licenses" \
    "${STAGE_DIR}/usr/lib/systemd/system" \
    "${STAGE_DIR}/usr/share/doc/${PKG_NAME}" \
    "${STAGE_DIR}/etc/go-flow-api" \
    "${STAGE_DIR}/etc/profile.d" \
    "${STAGE_DIR}/var/lib/go-flow-api" \
    "${STAGE_DIR}/var/log/go-flow-api" \
    "$DIST_DIR"

compile_flow_api_binary "linux" "$GOARCH_TARGET" "${STAGE_DIR}/opt/flow-api/bin"
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

cp "${PACKAGING_DIR}/services/systemd/go-flow-api.service" "${STAGE_DIR}/opt/flow-api/share/services/systemd/go-flow-api.service"
cp "${PACKAGING_DIR}/services/openrc/go-flow-api" "${STAGE_DIR}/opt/flow-api/share/services/openrc/go-flow-api"
cp "${PACKAGING_DIR}/services/sysvinit/go-flow-api" "${STAGE_DIR}/opt/flow-api/share/services/sysvinit/go-flow-api"
cp "${PACKAGING_DIR}/services/kerberos/go-flow-api-krb5-refresh.sh" "${STAGE_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh"
cp "${PACKAGING_DIR}/services/systemd/go-flow-api.service" "${STAGE_DIR}/usr/lib/systemd/system/go-flow-api.service"

cp "${REPO_ROOT}/LICENSE" "${STAGE_DIR}/opt/flow-api/share/licenses/LICENSE"
cp "${REPO_ROOT}/LICENSE" "${STAGE_DIR}/usr/share/doc/${PKG_NAME}/copyright"
cp "${PACKAGING_DIR}/etc/profile.d/go-flow-api.sh" "${STAGE_DIR}/etc/profile.d/go-flow-api.sh"
cp "${PACKAGING_DIR}/etc/go-flow-api/go-flow-api.env" "${STAGE_DIR}/etc/go-flow-api/go-flow-api.env"

cp "${PACKAGING_DIR}/debian/postinst" "${STAGE_DIR}/DEBIAN/postinst"
cp "${PACKAGING_DIR}/debian/prerm" "${STAGE_DIR}/DEBIAN/prerm"
cp "${PACKAGING_DIR}/debian/postrm" "${STAGE_DIR}/DEBIAN/postrm"

chmod 0755 \
    "${STAGE_DIR}/opt/flow-api/share/services/openrc/go-flow-api" \
    "${STAGE_DIR}/opt/flow-api/share/services/sysvinit/go-flow-api" \
    "${STAGE_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh" \
    "${STAGE_DIR}/DEBIAN/postinst" \
    "${STAGE_DIR}/DEBIAN/prerm" \
    "${STAGE_DIR}/DEBIAN/postrm"

chmod 0750 \
    "${STAGE_DIR}/etc/go-flow-api" \
    "${STAGE_DIR}/var/lib/go-flow-api" \
    "${STAGE_DIR}/var/log/go-flow-api"

chmod 0640 "${STAGE_DIR}/etc/go-flow-api/go-flow-api.env"

chmod 0644 \
    "${STAGE_DIR}/opt/flow-api/share/man/man1/"*.1.gz \
    "${STAGE_DIR}/opt/flow-api/share/man/man5/"*.5.gz \
    "${STAGE_DIR}/opt/flow-api/share/info/go-flow-api.info.gz" \
    "${STAGE_DIR}/opt/flow-api/share/info/dir" \
    "${STAGE_DIR}/opt/flow-api/share/sql/"*.sql \
    "${STAGE_DIR}/opt/flow-api/share/doc/"*.md \
    "${STAGE_DIR}/opt/flow-api/share/services/systemd/go-flow-api.service" \
    "${STAGE_DIR}/usr/lib/systemd/system/go-flow-api.service" \
    "${STAGE_DIR}/opt/flow-api/share/licenses/LICENSE" \
    "${STAGE_DIR}/usr/share/doc/${PKG_NAME}/copyright" \
    "${STAGE_DIR}/etc/profile.d/go-flow-api.sh"

INSTALLED_SIZE=$(du -sk "${STAGE_DIR}/opt" "${STAGE_DIR}/usr" "${STAGE_DIR}/etc" | awk '{s+=$1} END {print s}')

cat > "${STAGE_DIR}/DEBIAN/control" <<EOF
Package: ${PKG_NAME}
Version: ${FLOW_VERSION}-${FLOW_RELEASE}
Section: net
Priority: optional
Architecture: ${DEB_ARCH}
Installed-Size: ${INSTALLED_SIZE}
Maintainer: ${MAINTAINER}
Homepage: ${HOMEPAGE}
Description: ETL Madness go-flowAPI REST, Artifact CRUD, Execution & Telemetry Service (/opt/flow-api)
 Centralized HTTP REST API, database artifact repository, live SSE execution
 gateway, and multi-database telemetry daemon for the ETL Madness Flow ecosystem:
  - /opt/flow-api/bin/go-flowAPI: Pure-Go REST & SSE execution server supporting
    SQL Server, PostgreSQL, MySQL/MariaDB, SQLite, and Oracle.
  - /usr/lib/systemd/system/go-flow-api.service: Native systemd unit configured
    via /etc/go-flow-api/go-flow-api.env (plus OpenRC and SysVinit scripts).
EOF

cat > "${STAGE_DIR}/DEBIAN/conffiles" <<'EOF'
/etc/go-flow-api/go-flow-api.env
/etc/profile.d/go-flow-api.sh
EOF

chmod 0644 "${STAGE_DIR}/DEBIAN/control" "${STAGE_DIR}/DEBIAN/conffiles"

(
    cd "$STAGE_DIR"
    if command -v md5sum >/dev/null 2>&1; then
        find opt usr etc -type f -exec md5sum {} + > DEBIAN/md5sums
    elif command -v md5 >/dev/null 2>&1; then
        find opt usr etc -type f -exec md5 -r {} + | awk '{sum=$1; $1=""; sub(/^ /, ""); print sum "  " $0}' > DEBIAN/md5sums
    fi
)
[ -f "${STAGE_DIR}/DEBIAN/md5sums" ] && chmod 0644 "${STAGE_DIR}/DEBIAN/md5sums"

if command -v dpkg-deb >/dev/null 2>&1; then
    log_info "Building .deb package with dpkg-deb..."
    if dpkg-deb --help 2>&1 | grep -q -- '--root-owner-group'; then
        dpkg-deb --root-owner-group -Zgzip --build "$STAGE_DIR" "$OUT_DEB"
    elif command -v fakeroot >/dev/null 2>&1; then
        fakeroot dpkg-deb -Zgzip --build "$STAGE_DIR" "$OUT_DEB"
    else
        dpkg-deb -Zgzip --build "$STAGE_DIR" "$OUT_DEB"
    fi
elif command -v nfpm >/dev/null 2>&1; then
    log_info "dpkg-deb not found; falling back to nfpm..."
    REPO_ROOT="$REPO_ROOT" \
    PKG_NAME="$PKG_NAME" \
    STAGE_BIN_DIR="${STAGE_DIR}/opt/flow-api/bin" \
    STAGE_DOC_DIR="$DOC_TMP" \
    NFPM_ARCH="$DEB_ARCH" \
    FLOW_VERSION="$FLOW_VERSION" \
    FLOW_RELEASE="$FLOW_RELEASE" \
        nfpm package --config "${PACKAGING_DIR}/nfpm.yaml" --packager deb --target "$OUT_DEB"
else
    log_err "Neither dpkg-deb nor nfpm is installed. Install dpkg-deb (dpkg) or nfpm to build .deb packages."
    exit 1
fi

log_info "Created Debian package: ${OUT_DEB}"
