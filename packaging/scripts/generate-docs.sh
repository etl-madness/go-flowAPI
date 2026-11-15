#!/bin/sh
# Regenerates packaging/info/go-flow-api.info from packaging/info/go-flow-api.texi
# when GNU texinfo (makeinfo or texi2any) is installed; otherwise uses the bundled
# packaging/info/go-flow-api.info.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PACKAGING_DIR=$(CDPATH= cd -- "${SCRIPT_DIR}/.." && pwd)
TEXI_SRC="${PACKAGING_DIR}/info/go-flow-api.texi"
INFO_OUT="${PACKAGING_DIR}/info/go-flow-api.info"

if [ ! -f "$TEXI_SRC" ]; then
    printf '[ERROR] Missing Texinfo source: %s\n' "$TEXI_SRC" >&2
    exit 1
fi

if command -v makeinfo >/dev/null 2>&1; then
    printf '[INFO] Compiling %s -> %s using makeinfo...\n' "$TEXI_SRC" "$INFO_OUT"
    makeinfo --no-split -o "$INFO_OUT" "$TEXI_SRC"
elif command -v texi2any >/dev/null 2>&1; then
    printf '[INFO] Compiling %s -> %s using texi2any...\n' "$TEXI_SRC" "$INFO_OUT"
    texi2any --no-split -o "$INFO_OUT" "$TEXI_SRC"
elif [ -f "$INFO_OUT" ]; then
    printf '[INFO] makeinfo/texi2any not found; using pre-built %s\n' "$INFO_OUT"
else
    printf '[ERROR] Neither makeinfo/texi2any nor pre-built %s found.\n' "$INFO_OUT" >&2
    exit 1
fi
