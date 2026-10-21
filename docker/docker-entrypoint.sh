#!/bin/sh
set -e

# Automatically provision SQLite schema if FLOW_API_DSN targets a SQLite database
if [ -n "$FLOW_API_DSN" ] && [ -f "/app/docker/init/sqlite-init.sql" ]; then
    DB_PATH=""
    case "$FLOW_API_DSN" in
        sqlite://*)
            DB_PATH="${FLOW_API_DSN#sqlite://}"
            ;;
        sqlite3://*)
            DB_PATH="${FLOW_API_DSN#sqlite3://}"
            ;;
        sql://sqlite@*)
            DB_PATH="${FLOW_API_DSN#sql://sqlite@}"
            ;;
        db://sqlite@*)
            DB_PATH="${FLOW_API_DSN#db://sqlite@}"
            ;;
        *.db|*.sqlite|*.sqlite3)
            DB_PATH="$FLOW_API_DSN"
            ;;
    esac

    if [ -n "$DB_PATH" ] && [ "$DB_PATH" != ":memory:" ]; then
        mkdir -p "$(dirname "$DB_PATH")"
        echo "[entrypoint] Ensuring SQLite schema is initialized in $DB_PATH..."
        sqlite3 "$DB_PATH" < /app/docker/init/sqlite-init.sql
    fi
fi

exec /app/go-flowAPI "$@"
