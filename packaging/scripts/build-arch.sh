#!/bin/sh
# Builds an Arch Linux (.pkg.tar.zst) package for ETL Madness go-flowAPI.
# Installs binary (go-flowAPI), man pages, GNU Info manual, SQL schema DDL,
# reference guides, and systemd/OpenRC/SysVinit service files under /opt/flow-api,
# plus /usr/lib/systemd/system/go-flow-api.service, /etc/go-flow-api/go-flow-api.env,
# and /etc/profile.d/go-flow-api.sh.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

RAW_VERSION=$(resolve_version)
# Arch pkgver forbids hyphens '-'; replace hyphens with underscores '_'.
ARCH_VERSION=$(printf '%s' "$RAW_VERSION" | tr '-' '_')
GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-}}")

case "$GOARCH_TARGET" in
    amd64) ARCH_CARCH="x86_64" ;;
    arm64) ARCH_CARCH="aarch64" ;;
    *)
        log_err "Unsupported Arch Linux architecture: $GOARCH_TARGET"
        exit 1
        ;;
esac

WORK_DIR="${BUILD_ROOT}/arch-${ARCH_CARCH}"
STAGE_SRC="${WORK_DIR}/stage"
MAKEPKG_DIR="${WORK_DIR}/makepkg"
OUT_PKG="${DIST_DIR}/${PKG_NAME}-${ARCH_VERSION}-${FLOW_RELEASE}-${ARCH_CARCH}.pkg.tar.zst"

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
    "$MAKEPKG_DIR" \
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

build_with_nfpm() {
    REPO_ROOT="$REPO_ROOT" \
    STAGE_BIN_DIR="${STAGE_SRC}/bin" \
    STAGE_DOC_DIR="${STAGE_SRC}/doc" \
    NFPM_ARCH="$GOARCH_TARGET" \
    FLOW_VERSION="$ARCH_VERSION" \
    FLOW_RELEASE="$FLOW_RELEASE" \
        nfpm package --config "${PACKAGING_DIR}/nfpm.yaml" --packager archlinux --target "$OUT_PKG"
}

if command -v makepkg >/dev/null 2>&1 && [ "$(id -u)" -ne 0 ]; then
    TARBALL="${MAKEPKG_DIR}/go-flow-api-${ARCH_VERSION}-staged.tar.gz"
    (
        cd "$STAGE_SRC"
        tar -czf "$TARBALL" bin doc sql guides services profile.d etc-env LICENSE
    )
    sed \
        -e "s/__PKGVER__/${ARCH_VERSION}/g" \
        -e "s/__PKGREL__/${FLOW_RELEASE}/g" \
        -e "s/__CARCH__/${ARCH_CARCH}/g" \
        "${PACKAGING_DIR}/arch/PKGBUILD.tmpl" > "${MAKEPKG_DIR}/PKGBUILD"
    cp "${PACKAGING_DIR}/arch/go-flow-api.install" "${MAKEPKG_DIR}/go-flow-api.install"

    log_info "Building Arch Linux package with makepkg (${ARCH_CARCH})..."
    (
        cd "$MAKEPKG_DIR"
        CARCH="$ARCH_CARCH" PKGEXT='.pkg.tar.zst' makepkg -f --nodeps
    )

    BUILT_PKG=$(find "$MAKEPKG_DIR" -name '*.pkg.tar.*' -type f | head -n 1)
    if [ -z "$BUILT_PKG" ]; then
        log_err "makepkg succeeded but no .pkg.tar.* file was found in ${MAKEPKG_DIR}"
        exit 1
    fi
    cp "$BUILT_PKG" "$OUT_PKG"
elif command -v nfpm >/dev/null 2>&1; then
    if command -v makepkg >/dev/null 2>&1 && [ "$(id -u)" -eq 0 ]; then
        log_warn "Running as root (makepkg disallows root); building Arch package via nfpm..."
    else
        log_info "makepkg not found; falling back to nfpm..."
    fi
    build_with_nfpm
else
    if command -v makepkg >/dev/null 2>&1 && [ "$(id -u)" -eq 0 ]; then
        log_err "makepkg cannot be run as root. Run build-arch.sh as a non-root user, or install nfpm."
    else
        log_err "Neither makepkg nor nfpm is installed. Install pacman/makepkg (on Arch) or nfpm to build .pkg.tar.zst packages."
    fi
    exit 1
fi

log_info "Created Arch Linux package: ${OUT_PKG}"
