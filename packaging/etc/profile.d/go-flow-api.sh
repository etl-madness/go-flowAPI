# ETL Madness Flow API environment setup (/opt/flow-api).
# Appends /opt/flow-api/bin to PATH so existing system binaries are never shadowed.

case ":${PATH:-}:" in
    *:/opt/flow-api/bin:*) ;;
    *) export PATH="${PATH:+$PATH:}/opt/flow-api/bin" ;;
esac

# Expose /opt/flow-api/share/info to GNU info while retaining default system paths
# via the trailing colon.
case ":${INFOPATH:-}:" in
    *:/opt/flow-api/share/info:*) ;;
    *) export INFOPATH="${INFOPATH%:}${INFOPATH:+:}/opt/flow-api/share/info:" ;;
esac
