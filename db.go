package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb/azuread"
	_ "github.com/sijms/go-ora/v2"
	_ "modernc.org/sqlite"
)

// ParseDSN extracts the sql.Open driver name, canonical dialect type, and clean DSN
// from any go-flow connection string format (including sql://driver@dsn, db://driver@dsn,
// Azure AD fedauth=, postgres://, mysql://, oracle://, sqlserver://, and sqlite files).
func ParseDSN(rawDSN string) (sqlDriver string, dialect string, cleanDSN string) {
	dsn := strings.TrimSpace(rawDSN)
	driverHint := ""

	if strings.HasPrefix(dsn, "sql://") || strings.HasPrefix(dsn, "db://") {
		stripped := strings.TrimPrefix(strings.TrimPrefix(dsn, "sql://"), "db://")
		parts := strings.SplitN(stripped, "@", 2)
		if len(parts) == 2 {
			driverHint = strings.ToLower(strings.TrimSpace(parts[0]))
			dsn = parts[1]
		}
	}

	if driverHint == "" {
		driverHint = detectDriverFromDSN(dsn)
	}

	switch strings.ToLower(driverHint) {
	case "azuresql", "azuread":
		return "azuresql", "sqlserver", dsn
	case "sqlserver", "mssql":
		if strings.Contains(strings.ToLower(dsn), "fedauth=") || strings.Contains(strings.ToLower(dsn), "activedirectory") {
			return "azuresql", "sqlserver", dsn
		}
		return "sqlserver", "sqlserver", dsn
	case "postgres", "postgresql", "pq", "pgx":
		return "postgres", "postgres", dsn
	case "mysql", "mariadb":
		clean := strings.TrimPrefix(dsn, "mysql://")
		return "mysql", "mysql", clean
	case "sqlite", "sqlite3":
		clean := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite://"), "sqlite3://")
		return "sqlite", "sqlite", clean
	case "oracle", "ora", "godror":
		return "oracle", "oracle", dsn
	default:
		return driverHint, driverHint, dsn
	}
}

func detectDriverFromDSN(dsn string) string {
	lowerDSN := strings.ToLower(strings.TrimSpace(dsn))
	switch {
	case strings.Contains(lowerDSN, "fedauth=") || strings.Contains(lowerDSN, "activedirectory"):
		return "azuresql"
	case strings.HasPrefix(lowerDSN, "postgres://"), strings.HasPrefix(lowerDSN, "postgresql://"), strings.Contains(lowerDSN, "dbname="):
		return "postgres"
	case strings.HasPrefix(lowerDSN, "sqlserver://"), strings.Contains(lowerDSN, "server="):
		return "sqlserver"
	case strings.Contains(lowerDSN, "@tcp("), strings.HasPrefix(lowerDSN, "mysql://"):
		return "mysql"
	case lowerDSN == ":memory:",
		strings.HasPrefix(lowerDSN, "sqlite://"),
		strings.HasPrefix(lowerDSN, "sqlite3://"),
		strings.HasPrefix(lowerDSN, "file:"),
		strings.HasSuffix(lowerDSN, ".db"),
		strings.HasSuffix(lowerDSN, ".sqlite"),
		strings.HasSuffix(lowerDSN, ".sqlite3"):
		return "sqlite"
	case strings.HasPrefix(lowerDSN, "oracle://"):
		return "oracle"
	default:
		return "sqlserver"
	}
}

// RedactDSN masks any password in a database connection string for safe logging.
func RedactDSN(rawDSN string) string {
	return redactNodeOutput(rawDSN)
}

// OpenDatabase opens a database connection using any supported go-flow DSN format.
func OpenDatabase(rawDSN string) (*sql.DB, string, error) {
	sqlDriver, dialect, cleanDSN := ParseDSN(rawDSN)
	db, err := sql.Open(sqlDriver, cleanDSN)
	if err != nil {
		return nil, "", fmt.Errorf("failed opening %s database: %w", sqlDriver, err)
	}
	return db, dialect, nil
}

// Placeholder returns the dialect-specific positional parameter placeholder (1-indexed).
func Placeholder(dialect string, index int) string {
	switch strings.ToLower(dialect) {
	case "postgres":
		return fmt.Sprintf("$%d", index)
	case "sqlserver", "azuresql":
		return fmt.Sprintf("@p%d", index)
	case "oracle":
		return fmt.Sprintf(":%d", index)
	default:
		return "?"
	}
}

// BuildInsertQuery constructs a dialect-aware parameterized INSERT statement.
func BuildInsertQuery(dialect, tableConst string, columns []string) string {
	table := resolveTableName(dialect, tableConst)
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = Placeholder(dialect, i+1)
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)
}

func defaultApplicationName() string {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return "go-flowAPI"
	}
	base := filepath.Base(exe)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		return "go-flowAPI"
	}
	return name
}

// FetchDatabaseSessionIdentity queries the active database connection for its SPID/PID,
// authenticated database user, and session application name across all 5 supported engines.
func FetchDatabaseSessionIdentity(ctx context.Context, db *sql.DB, dialect string) (spid int64, dbUser string, appName string, err error) {
	defaultApp := defaultApplicationName()
	if db == nil {
		return int64(os.Getpid()), "", defaultApp, nil
	}

	switch strings.ToLower(dialect) {
	case "sqlserver", "azuresql":
		var pid sql.NullInt64
		var u, a sql.NullString
		err = db.QueryRowContext(ctx, "SELECT CAST(@@SPID AS BIGINT), CAST(SYSTEM_USER AS NVARCHAR(256)), CAST(ISNULL(APP_NAME(), '') AS NVARCHAR(256))").Scan(&pid, &u, &a)
		spid = pid.Int64
		dbUser = strings.TrimSpace(u.String)
		appName = strings.TrimSpace(a.String)
	case "postgres":
		var pid sql.NullInt64
		var u, a sql.NullString
		err = db.QueryRowContext(ctx, "SELECT CAST(pg_backend_pid() AS BIGINT), CAST(CURRENT_USER AS TEXT), COALESCE(current_setting('application_name', true), '')").Scan(&pid, &u, &a)
		spid = pid.Int64
		dbUser = strings.TrimSpace(u.String)
		appName = strings.TrimSpace(a.String)
	case "mysql":
		var pid sql.NullInt64
		var u sql.NullString
		err = db.QueryRowContext(ctx, "SELECT CAST(CONNECTION_ID() AS UNSIGNED), CURRENT_USER()").Scan(&pid, &u)
		spid = pid.Int64
		dbUser = strings.TrimSpace(u.String)
	case "oracle":
		var pid sql.NullInt64
		var u, a sql.NullString
		err = db.QueryRowContext(ctx, "SELECT TO_NUMBER(SYS_CONTEXT('USERENV', 'SID')), SYS_CONTEXT('USERENV', 'SESSION_USER'), COALESCE(SYS_CONTEXT('USERENV', 'MODULE'), SYS_CONTEXT('USERENV', 'CLIENT_PROGRAM_NAME'), '') FROM DUAL").Scan(&pid, &u, &a)
		spid = pid.Int64
		dbUser = strings.TrimSpace(u.String)
		appName = strings.TrimSpace(a.String)
	case "sqlite", "sqlite3":
		return int64(os.Getpid()), "sqlite", defaultApp, nil
	default:
		return int64(os.Getpid()), "", defaultApp, nil
	}

	if appName == "" {
		appName = defaultApp
	}
	if spid == 0 {
		spid = int64(os.Getpid())
	}
	return spid, dbUser, appName, err
}
