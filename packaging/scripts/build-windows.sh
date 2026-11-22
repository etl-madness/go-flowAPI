#!/bin/sh
# Cross-builds Windows WinGet-compatible packages (.zip portable archive and
# optional .msi installer via msitools 'wixl' or WiX 'wix') and generates
# WinGet 1.6.0 YAML manifests for ETLMadness.GoFlowAPI from Linux, macOS, or FreeBSD hosts.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

FLOW_VERSION=$(resolve_version)
GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-amd64}}")
WIN_FORMAT="${WIN_FORMAT:-all}"
RELEASE_BASE_URL="${RELEASE_BASE_URL:-https://github.com/etl-madness/go-flowAPI/releases/download/v${FLOW_VERSION}}"
RELEASE_BASE_URL="${RELEASE_BASE_URL%/}"
UPGRADE_CODE="{4B9E2C71-8A54-4D39-B61E-9C20F5A4D831}"

case "$GOARCH_TARGET" in
    amd64)
        WINGET_ARCH="x64"
        WIX_ARCH="x64"
        ;;
    arm64)
        WINGET_ARCH="arm64"
        WIX_ARCH="arm64"
        ;;
    *)
        log_err "Unsupported Windows architecture: $GOARCH_TARGET (expected amd64 or arm64)"
        exit 1
        ;;
esac

MSI_VERSION=$(printf '%s' "$FLOW_VERSION" | sed 's/[-+].*//' | awk -F. '{printf "%d.%d.%d", ($1+0), ($2+0), ($3+0)}')

sha256_upper() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print toupper($1)}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print toupper($1)}'
    elif command -v sha256 >/dev/null 2>&1; then
        sha256 -q "$1" | tr '[:lower:]' '[:upper:]'
    else
        log_err "No SHA-256 tool found (sha256sum, shasum, or sha256)"
        exit 1
    fi
}

deterministic_product_code() {
    seed_hex=$(printf 'ETLMadness.GoFlowAPI.MSI:%s:%s' "$FLOW_VERSION" "$GOARCH_TARGET" | {
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum | awk '{print toupper($1)}'
        elif command -v shasum >/dev/null 2>&1; then
            shasum -a 256 | awk '{print toupper($1)}'
        else
            sha256 -q | tr '[:lower:]' '[:upper:]'
        fi
    })
    p1=$(printf '%s' "$seed_hex" | cut -c1-8)
    p2=$(printf '%s' "$seed_hex" | cut -c9-12)
    p3="5$(printf '%s' "$seed_hex" | cut -c14-16)"
    p4="9$(printf '%s' "$seed_hex" | cut -c18-20)"
    p5=$(printf '%s' "$seed_hex" | cut -c21-32)
    printf '{%s-%s-%s-%s-%s}' "$p1" "$p2" "$p3" "$p4" "$p5"
}

WORK_DIR="${BUILD_ROOT}/windows-${GOARCH_TARGET}"
STAGE_DIR="${WORK_DIR}/payload"

rm -rf "$WORK_DIR"
mkdir -p \
    "${STAGE_DIR}/bin" \
    "${STAGE_DIR}/docs" \
    "${STAGE_DIR}/share/sql" \
    "${STAGE_DIR}/share/services/windows" \
    "${STAGE_DIR}/share/man/man1" \
    "${STAGE_DIR}/share/man/man5" \
    "$DIST_DIR"

log_info "Cross-compiling go-flowAPI.exe for windows/${GOARCH_TARGET}..."
(
    cd "$REPO_ROOT"
    CGO_ENABLED=0 GOOS="windows" GOARCH="$GOARCH_TARGET" \
        go build -trimpath -ldflags="-s -w" -o "${STAGE_DIR}/bin/go-flowAPI.exe" .
)

cp "${REPO_ROOT}/SCHEMA.md" "${STAGE_DIR}/docs/SCHEMA.md"
cp "${REPO_ROOT}/BOOTSTRAP.md" "${STAGE_DIR}/docs/BOOTSTRAP.md"
cp "${REPO_ROOT}/docs/API_ENDPOINTS_REFERENCE.md" "${STAGE_DIR}/docs/API_ENDPOINTS_REFERENCE.md"
cp "${REPO_ROOT}/docs/AD_KERBEROS_SIDECAR_GUIDE.md" "${STAGE_DIR}/docs/AD_KERBEROS_SIDECAR_GUIDE.md"

cp "${REPO_ROOT}/docker/init/sqlite-init.sql" "${STAGE_DIR}/share/sql/sqlite-init.sql"
cp "${REPO_ROOT}/docker/init/postgres-init.sql" "${STAGE_DIR}/share/sql/postgres-init.sql"
cp "${REPO_ROOT}/docker/init/sqlserver-init.sql" "${STAGE_DIR}/share/sql/sqlserver-init.sql"

cp "${PACKAGING_DIR}/services/windows/Install-GoFlowAPIService.ps1" "${STAGE_DIR}/share/services/windows/Install-GoFlowAPIService.ps1"
cp "${PACKAGING_DIR}/services/windows/Uninstall-GoFlowAPIService.ps1" "${STAGE_DIR}/share/services/windows/Uninstall-GoFlowAPIService.ps1"
sed 's|sqlite:///var/lib/go-flow-api/flow_api.db|sqlite://C:/ProgramData/ETL Madness/Flow API/data/flow_api.db|g' \
    "${PACKAGING_DIR}/etc/go-flow-api/go-flow-api.env" > "${STAGE_DIR}/share/services/windows/go-flow-api.env.example"

cp "${PACKAGING_DIR}/man/man1/go-flowAPI.1" "${STAGE_DIR}/share/man/man1/go-flowAPI.1"
cp "${PACKAGING_DIR}/man/man5/go-flow-api.env.5" "${STAGE_DIR}/share/man/man5/go-flow-api.env.5"
cp "${REPO_ROOT}/LICENSE" "${STAGE_DIR}/LICENSE.txt"

INSTALLERS_TMP="${WORK_DIR}/installers.yaml.part"
: > "$INSTALLERS_TMP"

if [ "$WIN_FORMAT" = "all" ] || [ "$WIN_FORMAT" = "zip" ]; then
    ZIP_NAME="${PKG_NAME}-${FLOW_VERSION}-windows-${GOARCH_TARGET}.zip"
    OUT_ZIP="${DIST_DIR}/${ZIP_NAME}"
    rm -f "$OUT_ZIP"
    log_info "Creating portable Windows archive: ${OUT_ZIP}"
    if command -v zip >/dev/null 2>&1; then
        (cd "$STAGE_DIR" && zip -q -r "$OUT_ZIP" .)
    elif command -v python3 >/dev/null 2>&1; then
        python3 - "$STAGE_DIR" "$OUT_ZIP" <<'PY'
import os, sys, zipfile
stage, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zf:
    for root, _, files in os.walk(stage):
        for f in sorted(files):
            full = os.path.join(root, f)
            rel = os.path.relpath(full, stage).replace(os.sep, "/")
            zf.write(full, rel)
PY
    else
        log_err "Neither 'zip' nor 'python3' is installed to create .zip archives."
        exit 1
    fi
    ZIP_SHA=$(sha256_upper "$OUT_ZIP")
    cat >> "$INSTALLERS_TMP" <<EOF
  - Architecture: ${WINGET_ARCH}
    InstallerType: zip
    NestedInstallerType: portable
    NestedInstallerFiles:
      - RelativeFilePath: bin\go-flowAPI.exe
        PortableCommandAlias: go-flowAPI
    InstallerUrl: ${RELEASE_BASE_URL}/${ZIP_NAME}
    InstallerSha256: ${ZIP_SHA}
EOF
fi

if [ "$WIN_FORMAT" = "all" ] || [ "$WIN_FORMAT" = "msi" ]; then
    MSI_NAME="${PKG_NAME}-${FLOW_VERSION}-windows-${GOARCH_TARGET}.msi"
    OUT_MSI="${DIST_DIR}/${MSI_NAME}"
    PRODUCT_CODE=$(deterministic_product_code)
    MSI_BUILT=0

    if command -v wix >/dev/null 2>&1; then
        log_info "Building Windows MSI with wix (${WIX_ARCH})..."
        wix build "${PACKAGING_DIR}/windows/go-flow-api.wxs" \
            -arch "$WIX_ARCH" \
            -d "StageDir=${STAGE_DIR}" \
            -d "FlowMsiVersion=${MSI_VERSION}" \
            -d "FlowProductCode=${PRODUCT_CODE}" \
            -o "$OUT_MSI"
        MSI_BUILT=1
    elif command -v wixl >/dev/null 2>&1; then
        log_info "Building Windows MSI with msitools wixl (${WIX_ARCH})..."
        wixl --arch "$WIX_ARCH" \
            -D "StageDir=${STAGE_DIR}" \
            -D "FlowMsiVersion=${MSI_VERSION}" \
            -D "FlowProductCode=${PRODUCT_CODE}" \
            -o "$OUT_MSI" "${PACKAGING_DIR}/windows/go-flow-api-wix3.wxs"
        MSI_BUILT=1
    elif [ "$WIN_FORMAT" = "msi" ]; then
        log_err "Neither 'wix' nor 'wixl' (msitools) is installed to build .msi packages."
        exit 1
    else
        log_warn "Neither 'wix' nor 'wixl' (msitools) found; skipping .msi build (.zip and WinGet manifests generated)."
    fi

    if [ "$MSI_BUILT" -eq 1 ] && [ -f "$OUT_MSI" ]; then
        MSI_SHA=$(sha256_upper "$OUT_MSI")
        cat >> "$INSTALLERS_TMP" <<EOF
  - Architecture: ${WINGET_ARCH}
    Scope: machine
    InstallerType: wix
    InstallerUrl: ${RELEASE_BASE_URL}/${MSI_NAME}
    InstallerSha256: ${MSI_SHA}
    ProductCode: '${PRODUCT_CODE}'
    AppsAndFeaturesEntries:
      - DisplayName: ETL Madness Flow API
        Publisher: ETL Madness
        DisplayVersion: ${MSI_VERSION}
        ProductCode: '${PRODUCT_CODE}'
        UpgradeCode: '${UPGRADE_CODE}'
EOF
    fi
fi

WINGET_DIR="${DIST_DIR}/winget/e/ETLMadness/GoFlowAPI/${FLOW_VERSION}"
mkdir -p "$WINGET_DIR"

cat > "${WINGET_DIR}/ETLMadness.GoFlowAPI.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.1.6.0.schema.json

PackageIdentifier: ETLMadness.GoFlowAPI
PackageVersion: ${FLOW_VERSION}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.6.0
EOF

cat > "${WINGET_DIR}/ETLMadness.GoFlowAPI.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.1.6.0.schema.json

PackageIdentifier: ETLMadness.GoFlowAPI
PackageVersion: ${FLOW_VERSION}
PackageLocale: en-US
Publisher: ETL Madness
PublisherUrl: https://github.com/etl-madness
PublisherSupportUrl: https://github.com/etl-madness/go-flowAPI/issues
PackageName: ETL Madness Flow API
PackageUrl: https://github.com/etl-madness/go-flowAPI
License: MIT
LicenseUrl: https://github.com/etl-madness/go-flowAPI/blob/main/LICENSE
ShortDescription: Centralized REST API, artifact repository, SSE execution gateway, and telemetry daemon for Flow.
Description: >-
  ETL Madness Flow API (go-flowAPI) is a pure-Go REST API and native Windows Service
  for managing Flow database artifacts (pipelines, options, configs), executing and
  validating pipelines with live Server-Sent Events (SSE) streaming, performing
  cryptographic operations, and recording audit and execution telemetry across SQL Server,
  PostgreSQL, MySQL/MariaDB, SQLite, and Oracle.
Moniker: go-flow-api
Tags:
  - api
  - database
  - daemon
  - etl
  - golang
  - rest
  - sql
  - workflow
Documentations:
  - DocumentLabel: Online Documentation
    DocumentUrl: https://etl-madness.github.io/flow-docs/
ManifestType: defaultLocale
ManifestVersion: 1.6.0
EOF

{
    cat <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.1.6.0.schema.json

PackageIdentifier: ETLMadness.GoFlowAPI
PackageVersion: ${FLOW_VERSION}
Commands:
  - go-flowAPI
Installers:
EOF
    cat "$INSTALLERS_TMP"
    cat <<'EOF'
ManifestType: installer
ManifestVersion: 1.6.0
EOF
} > "${WINGET_DIR}/ETLMadness.GoFlowAPI.installer.yaml"

log_info "Generated WinGet manifests in: ${WINGET_DIR}"
