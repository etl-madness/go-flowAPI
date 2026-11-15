#!/bin/sh
# Builds an RPM (.rpm) package for ETL Madness go-flowAPI (RHEL, Fedora, SUSE, openSUSE).
# Installs binary (go-flowAPI), man pages, GNU Info manual, SQL schema DDL,
# reference guides, and systemd/OpenRC/SysVinit service scripts under /opt/flow-api,
# plus /usr/lib/systemd/system/go-flow-api.service, /etc/go-flow-api/go-flow-api.env,
# and /etc/profile.d/go-flow-api.sh.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

RAW_VERSION=$(resolve_version)
# RPM Version field forbids '-'; replace hyphens with '~' for pre-releases.
RPM_VERSION=$(printf '%s' "$RAW_VERSION" | tr '-' '~')
GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-}}")

case "$GOARCH_TARGET" in
    amd64) RPM_ARCH="x86_64" ;;
    arm64) RPM_ARCH="aarch64" ;;
    *)
        log_err "Unsupported .rpm architecture: $GOARCH_TARGET"
        exit 1
        ;;
esac

WORK_DIR="${BUILD_ROOT}/rpm-${RPM_ARCH}"
STAGE_SRC="${WORK_DIR}/stage"
TOP_DIR="${WORK_DIR}/rpmbuild"

rm -rf "$WORK_DIR"
mkdir -p \
    "${STAGE_SRC}/bin" \
    "${STAGE_SRC}/doc" \
    "${STAGE_SRC}/sql" \
    "${STAGE_SRC}/guides" \
    "${STAGE_SRC}/services/systemd" \
    "${STAGE_SRC}/services/openrc" \
    "${STAGE_SRC}/services/sysvinit" \
    "${STAGE_SRC}/services/kerberos" \
    "${STAGE_SRC}/profile.d" \
    "${STAGE_SRC}/etc-env" \
    "${TOP_DIR}/BUILD" \
    "${TOP_DIR}/BUILDROOT" \
    "${TOP_DIR}/RPMS" \
    "${TOP_DIR}/SOURCES" \
    "${TOP_DIR}/SPECS" \
    "${TOP_DIR}/SRPMS" \
    "$DIST_DIR"

compile_flow_api_binary "linux" "$GOARCH_TARGET" "${STAGE_SRC}/bin"
stage_compressed_docs "${STAGE_SRC}/doc"

cp "${REPO_ROOT}/docker/init/sqlite-init.sql" "${STAGE_SRC}/sql/sqlite-init.sql"
cp "${REPO_ROOT}/docker/init/postgres-init.sql" "${STAGE_SRC}/sql/postgres-init.sql"
cp "${REPO_ROOT}/docker/init/sqlserver-init.sql" "${STAGE_SRC}/sql/sqlserver-init.sql"

cp "${REPO_ROOT}/SCHEMA.md" "${STAGE_SRC}/guides/SCHEMA.md"
cp "${REPO_ROOT}/BOOTSTRAP.md" "${STAGE_SRC}/guides/BOOTSTRAP.md"
cp "${REPO_ROOT}/docs/API_ENDPOINTS_REFERENCE.md" "${STAGE_SRC}/guides/API_ENDPOINTS_REFERENCE.md"
cp "${REPO_ROOT}/docs/AD_KERBEROS_SIDECAR_GUIDE.md" "${STAGE_SRC}/guides/AD_KERBEROS_SIDECAR_GUIDE.md"

cp "${PACKAGING_DIR}/services/systemd/go-flow-api.service" "${STAGE_SRC}/services/systemd/go-flow-api.service"
cp "${PACKAGING_DIR}/services/openrc/go-flow-api" "${STAGE_SRC}/services/openrc/go-flow-api"
cp "${PACKAGING_DIR}/services/sysvinit/go-flow-api" "${STAGE_SRC}/services/sysvinit/go-flow-api"
cp "${PACKAGING_DIR}/services/kerberos/go-flow-api-krb5-refresh.sh" "${STAGE_SRC}/services/kerberos/go-flow-api-krb5-refresh.sh"

cp "${REPO_ROOT}/LICENSE" "${STAGE_SRC}/LICENSE"
cp "${PACKAGING_DIR}/etc/profile.d/go-flow-api.sh" "${STAGE_SRC}/profile.d/go-flow-api.sh"
cp "${PACKAGING_DIR}/etc/go-flow-api/go-flow-api.env" "${STAGE_SRC}/etc-env/go-flow-api.env"

if command -v rpmbuild >/dev/null 2>&1; then
    TARBALL="${TOP_DIR}/SOURCES/go-flow-api-${RPM_VERSION}-staged.tar.gz"
    (
        cd "$STAGE_SRC"
        tar -czf "$TARBALL" bin doc sql guides services profile.d etc-env LICENSE
    )
    cp "${PACKAGING_DIR}/rpm/go-flow-api.spec" "${TOP_DIR}/SPECS/go-flow-api.spec"

    log_info "Building .rpm package with rpmbuild (${RPM_ARCH})..."
    rpmbuild -bb \
        --target "$RPM_ARCH" \
        --define "_topdir ${TOP_DIR}" \
        --define "_flow_version ${RPM_VERSION}" \
        --define "_flow_release ${FLOW_RELEASE}" \
        "${TOP_DIR}/SPECS/go-flow-api.spec"

    BUILT_RPM=$(find "${TOP_DIR}/RPMS" -name '*.rpm' -type f | head -n 1)
    if [ -z "$BUILT_RPM" ]; then
        log_err "rpmbuild succeeded but no .rpm artifact was found in ${TOP_DIR}/RPMS"
        exit 1
    fi
    OUT_RPM="${DIST_DIR}/$(basename "$BUILT_RPM")"
    cp "$BUILT_RPM" "$OUT_RPM"
elif command -v nfpm >/dev/null 2>&1; then
    log_info "rpmbuild not found; falling back to nfpm..."
    OUT_RPM="${DIST_DIR}/${PKG_NAME}-${RPM_VERSION}-${FLOW_RELEASE}.${RPM_ARCH}.rpm"
    REPO_ROOT="$REPO_ROOT" \
    STAGE_BIN_DIR="${STAGE_SRC}/bin" \
    STAGE_DOC_DIR="${STAGE_SRC}/doc" \
    NFPM_ARCH="$GOARCH_TARGET" \
    FLOW_VERSION="$RPM_VERSION" \
    FLOW_RELEASE="$FLOW_RELEASE" \
        nfpm package --config "${PACKAGING_DIR}/nfpm.yaml" --packager rpm --target "$OUT_RPM"
else
    log_err "Neither rpmbuild nor nfpm is installed. Install rpm-build or nfpm to build .rpm packages."
    exit 1
fi

log_info "Created RPM package: ${OUT_RPM}"
