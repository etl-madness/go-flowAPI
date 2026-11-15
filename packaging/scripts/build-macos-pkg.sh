#!/bin/sh
# Builds a macOS Installer (.pkg) package for ETL Madness go-flowAPI.
# Installs binary (go-flowAPI), man pages, GNU Info manual, SQL schema DDL,
# reference guides, LaunchDaemon plist, and go-flow-api.env.example under /opt/flow-api,
# plus path_helper drop-ins in /private/etc/paths.d/go-flow-api and
# /private/etc/manpaths.d/go-flow-api.
# Supports GOARCH=arm64, GOARCH=amd64, or GOARCH=universal (via lipo).

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

if ! command -v pkgbuild >/dev/null 2>&1 || ! command -v productbuild >/dev/null 2>&1; then
    log_err "pkgbuild and productbuild are required (run this script on macOS with Xcode Command Line Tools)."
    exit 1
fi

FLOW_VERSION=$(resolve_version)
GOARCH_TARGET=$(normalize_goarch "${GOARCH:-${1:-}}")

case "$GOARCH_TARGET" in
    arm64)
        PKG_ARCH="arm64"
        HOST_ARCHS="arm64"
        ;;
    amd64)
        PKG_ARCH="amd64"
        HOST_ARCHS="x86_64"
        ;;
    universal)
        if ! command -v lipo >/dev/null 2>&1; then
            log_err "lipo is required for GOARCH=universal builds."
            exit 1
        fi
        PKG_ARCH="universal"
        HOST_ARCHS="x86_64,arm64"
        ;;
    *)
        log_err "Unsupported macOS architecture: $GOARCH_TARGET"
        exit 1
        ;;
esac

WORK_DIR="${BUILD_ROOT}/macos-${PKG_ARCH}"
PAYLOAD_DIR="${WORK_DIR}/payload"
PKG_SCRIPTS_DIR="${WORK_DIR}/scripts"
DOC_TMP="${WORK_DIR}/docs"
COMPONENT_PKG="${WORK_DIR}/go-flow-api-component.pkg"
DIST_XML="${WORK_DIR}/Distribution.xml"
OUT_PKG="${DIST_DIR}/${PKG_NAME}-${FLOW_VERSION}-darwin-${PKG_ARCH}.pkg"

rm -rf "$WORK_DIR"
mkdir -p \
    "${PAYLOAD_DIR}/opt/flow-api/bin" \
    "${PAYLOAD_DIR}/opt/flow-api/etc" \
    "${PAYLOAD_DIR}/opt/flow-api/share/man/man1" \
    "${PAYLOAD_DIR}/opt/flow-api/share/man/man5" \
    "${PAYLOAD_DIR}/opt/flow-api/share/info" \
    "${PAYLOAD_DIR}/opt/flow-api/share/sql" \
    "${PAYLOAD_DIR}/opt/flow-api/share/doc" \
    "${PAYLOAD_DIR}/opt/flow-api/share/services/launchd" \
    "${PAYLOAD_DIR}/opt/flow-api/share/services/kerberos" \
    "${PAYLOAD_DIR}/opt/flow-api/share/licenses" \
    "${PAYLOAD_DIR}/private/etc/paths.d" \
    "${PAYLOAD_DIR}/private/etc/manpaths.d" \
    "$PKG_SCRIPTS_DIR" \
    "$DIST_DIR"

if [ "$GOARCH_TARGET" = "universal" ]; then
    compile_flow_api_binary "darwin" "amd64" "${WORK_DIR}/bin-amd64"
    compile_flow_api_binary "darwin" "arm64" "${WORK_DIR}/bin-arm64"
    lipo -create \
        "${WORK_DIR}/bin-amd64/go-flowAPI" \
        "${WORK_DIR}/bin-arm64/go-flowAPI" \
        -output "${PAYLOAD_DIR}/opt/flow-api/bin/go-flowAPI"
    chmod 0755 "${PAYLOAD_DIR}/opt/flow-api/bin/go-flowAPI"
else
    compile_flow_api_binary "darwin" "$GOARCH_TARGET" "${PAYLOAD_DIR}/opt/flow-api/bin"
fi

stage_compressed_docs "$DOC_TMP"

cp "${DOC_TMP}/man1/"*.1.gz "${PAYLOAD_DIR}/opt/flow-api/share/man/man1/"
cp "${DOC_TMP}/man5/"*.5.gz "${PAYLOAD_DIR}/opt/flow-api/share/man/man5/"
cp "${DOC_TMP}/info/go-flow-api.info.gz" "${PAYLOAD_DIR}/opt/flow-api/share/info/go-flow-api.info.gz"
cp "${DOC_TMP}/info/dir" "${PAYLOAD_DIR}/opt/flow-api/share/info/dir"

cp "${REPO_ROOT}/docker/init/sqlite-init.sql" "${PAYLOAD_DIR}/opt/flow-api/share/sql/sqlite-init.sql"
cp "${REPO_ROOT}/docker/init/postgres-init.sql" "${PAYLOAD_DIR}/opt/flow-api/share/sql/postgres-init.sql"
cp "${REPO_ROOT}/docker/init/sqlserver-init.sql" "${PAYLOAD_DIR}/opt/flow-api/share/sql/sqlserver-init.sql"

cp "${REPO_ROOT}/SCHEMA.md" "${PAYLOAD_DIR}/opt/flow-api/share/doc/SCHEMA.md"
cp "${REPO_ROOT}/BOOTSTRAP.md" "${PAYLOAD_DIR}/opt/flow-api/share/doc/BOOTSTRAP.md"
cp "${REPO_ROOT}/docs/API_ENDPOINTS_REFERENCE.md" "${PAYLOAD_DIR}/opt/flow-api/share/doc/API_ENDPOINTS_REFERENCE.md"
cp "${REPO_ROOT}/docs/AD_KERBEROS_SIDECAR_GUIDE.md" "${PAYLOAD_DIR}/opt/flow-api/share/doc/AD_KERBEROS_SIDECAR_GUIDE.md"

cp "${PACKAGING_DIR}/services/launchd/com.etlmadness.go-flow-api.plist" \
    "${PAYLOAD_DIR}/opt/flow-api/share/services/launchd/com.etlmadness.go-flow-api.plist"
cp "${PACKAGING_DIR}/services/kerberos/go-flow-api-krb5-refresh.sh" \
    "${PAYLOAD_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh"
chmod 0755 "${PAYLOAD_DIR}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh"
sed 's|/var/lib/go-flow-api|/var/db/go-flow-api|g' \
    "${PACKAGING_DIR}/etc/go-flow-api/go-flow-api.env" > "${PAYLOAD_DIR}/opt/flow-api/etc/go-flow-api.env.example"

cp "${REPO_ROOT}/LICENSE" "${PAYLOAD_DIR}/opt/flow-api/share/licenses/LICENSE"

printf '/opt/flow-api/bin\n' > "${PAYLOAD_DIR}/private/etc/paths.d/go-flow-api"
printf '/opt/flow-api/share/man\n' > "${PAYLOAD_DIR}/private/etc/manpaths.d/go-flow-api"

cat > "${PKG_SCRIPTS_DIR}/postinstall" <<'EOF'
#!/bin/sh
mkdir -p /var/db/go-flow-api /var/log/go-flow-api
chmod 0750 /var/db/go-flow-api /var/log/go-flow-api
if [ ! -f /opt/flow-api/etc/go-flow-api.env ] && [ -f /opt/flow-api/etc/go-flow-api.env.example ]; then
    cp /opt/flow-api/etc/go-flow-api.env.example /opt/flow-api/etc/go-flow-api.env
    chmod 0600 /opt/flow-api/etc/go-flow-api.env
fi
exit 0
EOF
chmod 0755 "${PKG_SCRIPTS_DIR}/postinstall"

chmod 0644 \
    "${PAYLOAD_DIR}/opt/flow-api/share/man/man1/"*.1.gz \
    "${PAYLOAD_DIR}/opt/flow-api/share/man/man5/"*.5.gz \
    "${PAYLOAD_DIR}/opt/flow-api/share/info/go-flow-api.info.gz" \
    "${PAYLOAD_DIR}/opt/flow-api/share/info/dir" \
    "${PAYLOAD_DIR}/opt/flow-api/share/sql/"*.sql \
    "${PAYLOAD_DIR}/opt/flow-api/share/doc/"*.md \
    "${PAYLOAD_DIR}/opt/flow-api/share/services/launchd/com.etlmadness.go-flow-api.plist" \
    "${PAYLOAD_DIR}/opt/flow-api/etc/go-flow-api.env.example" \
    "${PAYLOAD_DIR}/opt/flow-api/share/licenses/LICENSE" \
    "${PAYLOAD_DIR}/private/etc/paths.d/go-flow-api" \
    "${PAYLOAD_DIR}/private/etc/manpaths.d/go-flow-api"

sed \
    -e "s/__VERSION__/${FLOW_VERSION}/g" \
    -e "s/__HOST_ARCHS__/${HOST_ARCHS}/g" \
    "${PACKAGING_DIR}/macos/Distribution.xml.tmpl" > "$DIST_XML"

log_info "Building macOS component package..."
pkgbuild \
    --root "$PAYLOAD_DIR" \
    --scripts "$PKG_SCRIPTS_DIR" \
    --identifier "com.etlmadness.go-flow-api" \
    --version "$FLOW_VERSION" \
    --install-location "/" \
    "$COMPONENT_PKG"

log_info "Building macOS distribution package..."
if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
    productbuild \
        --distribution "$DIST_XML" \
        --package-path "$WORK_DIR" \
        --sign "$MACOS_SIGN_IDENTITY" \
        "$OUT_PKG"
else
    productbuild \
        --distribution "$DIST_XML" \
        --package-path "$WORK_DIR" \
        "$OUT_PKG"
fi

log_info "Created macOS package: ${OUT_PKG}"
