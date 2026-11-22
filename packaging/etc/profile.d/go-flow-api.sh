# ETL Madness Flow API environment setup (/opt/flow-api).
# Appends /opt/flow-api/bin to PATH so existing system binaries are never shadowed.
# Safe under POSIX sh, ksh, bash, dash, and zsh, including 'set -u' (nounset).

case ":${PATH:-}:" in
    *:/opt/flow-api/bin:*) ;;
    *)
        if [ -n "${PATH:-}" ]; then
            export PATH="${PATH}:/opt/flow-api/bin"
        else
            export PATH="/opt/flow-api/bin"
        fi
        ;;
esac

# Expose /opt/flow-api/share/info to GNU info while retaining default system paths
# via the trailing colon.
case ":${INFOPATH:-}:" in
    *:/opt/flow-api/share/info:*) ;;
    *)
        _goflowapi_infopath="${INFOPATH:-}"
        _goflowapi_infopath="${_goflowapi_infopath%:}"
        if [ -n "$_goflowapi_infopath" ]; then
            export INFOPATH="${_goflowapi_infopath}:/opt/flow-api/share/info:"
        else
            export INFOPATH="/opt/flow-api/share/info:"
        fi
        unset _goflowapi_infopath
        ;;
esac

# On OpenBSD, man(1) does not auto-derive ../share/man from PATH; a leading
# colon in MANPATH appends to the default /etc/man.conf search path.
if [ "$(uname -s 2>/dev/null || true)" = "OpenBSD" ]; then
    case ":${MANPATH:-}:" in
        *:/opt/flow-api/share/man:*) ;;
        *)
            if [ -n "${MANPATH:-}" ]; then
                export MANPATH="${MANPATH}:/opt/flow-api/share/man"
            else
                export MANPATH=":/opt/flow-api/share/man"
            fi
            ;;
    esac
fi
