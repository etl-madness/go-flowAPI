#!/bin/sh
# Orchestrates building ETL Madness go-flowAPI OS packages.
#
# Usage:
#   ./packaging/scripts/build-all.sh [host|all|deb|rpm|arch|macos|freebsd|windows] [amd64|arm64|universal]
#
# Examples:
#   ./packaging/scripts/build-all.sh host          # Build native package(s) for current OS
#   ./packaging/scripts/build-all.sh deb amd64     # Build Linux .deb for amd64
#   ./packaging/scripts/build-all.sh rpm arm64     # Build Linux .rpm for aarch64 (RHEL/Fedora/SUSE/openSUSE)
#   ./packaging/scripts/build-all.sh arch amd64    # Build Arch Linux .pkg.tar.zst for x86_64
#   ./packaging/scripts/build-all.sh macos universal
#   ./packaging/scripts/build-all.sh freebsd amd64
#   ./packaging/scripts/build-all.sh windows amd64 # Cross-build Windows .zip/.msi + WinGet manifests

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=packaging/scripts/common.sh
. "${SCRIPT_DIR}/common.sh"

TARGET="${1:-host}"
ARCH_ARG="${2:-${GOARCH:-}}"

run_target() {
    t="$1"
    if [ -n "$ARCH_ARG" ]; then
        GOARCH="$ARCH_ARG" sh "${SCRIPT_DIR}/build-${t}.sh" "$ARCH_ARG"
    else
        sh "${SCRIPT_DIR}/build-${t}.sh"
    fi
}

case "$TARGET" in
    deb)
        run_target "deb"
        ;;
    rpm)
        run_target "rpm"
        ;;
    arch|archlinux|pacman)
        run_target "arch"
        ;;
    macos|pkg-macos|darwin)
        run_target "macos-pkg"
        ;;
    freebsd|pkg-freebsd)
        run_target "freebsd-pkg"
        ;;
    windows|winget|win)
        run_target "windows"
        ;;
    host)
        HOST_OS=$(uname -s)
        case "$HOST_OS" in
            Linux)
                built=0
                if command -v dpkg-deb >/dev/null 2>&1 || command -v nfpm >/dev/null 2>&1; then
                    run_target "deb"
                    built=1
                fi
                if command -v rpmbuild >/dev/null 2>&1 || command -v nfpm >/dev/null 2>&1; then
                    run_target "rpm"
                    built=1
                fi
                if command -v makepkg >/dev/null 2>&1 || command -v nfpm >/dev/null 2>&1; then
                    run_target "arch"
                    built=1
                fi
                if [ "$built" -eq 0 ]; then
                    log_err "No Linux packager found (install dpkg-deb, rpmbuild, makepkg, or nfpm)."
                    exit 1
                fi
                ;;
            Darwin)
                run_target "macos-pkg"
                ;;
            FreeBSD)
                run_target "freebsd-pkg"
                ;;
            *)
                log_err "Unsupported host OS for automatic detection: $HOST_OS"
                exit 1
                ;;
        esac
        ;;
    all)
        if command -v dpkg-deb >/dev/null 2>&1 || command -v nfpm >/dev/null 2>&1; then
            run_target "deb"
        else
            log_warn "Skipping .deb (neither dpkg-deb nor nfpm installed)"
        fi
        if command -v rpmbuild >/dev/null 2>&1 || command -v nfpm >/dev/null 2>&1; then
            run_target "rpm"
        else
            log_warn "Skipping .rpm (neither rpmbuild nor nfpm installed)"
        fi
        if command -v makepkg >/dev/null 2>&1 || command -v nfpm >/dev/null 2>&1; then
            run_target "arch"
        else
            log_warn "Skipping Arch .pkg.tar.zst (neither makepkg nor nfpm installed)"
        fi
        if [ "$(uname -s)" = "Darwin" ] && command -v pkgbuild >/dev/null 2>&1; then
            run_target "macos-pkg"
        else
            log_warn "Skipping macOS .pkg (requires macOS pkgbuild/productbuild)"
        fi
        if [ "$(uname -s)" = "FreeBSD" ] && command -v pkg >/dev/null 2>&1; then
            run_target "freebsd-pkg"
        else
            log_warn "Skipping FreeBSD .pkg (requires FreeBSD pkg)"
        fi
        run_target "windows"
        ;;
    *)
        log_err "Unknown target: $TARGET (expected: host, all, deb, rpm, arch, macos, freebsd, windows)"
        exit 1
        ;;
esac
