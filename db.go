package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb/azuread"
	"github.com/microsoft/go-mssqldb/integratedauth"
	"github.com/microsoft/go-mssqldb/integratedauth/krb5"
	"github.com/microsoft/go-mssqldb/msdsn"
	_ "github.com/sijms/go-ora/v2"
	_ "modernc.org/sqlite"
)

func init() {
	configureUnixKerberosSQLServerAuth()
}

// configureUnixKerberosSQLServerAuth registers a smart integrated authenticator on non-Windows platforms
// (Linux/macOS/FreeBSD containers) that normalizes KRB5CCNAME ("FILE:/path" -> "/path") for gokrb5 and
// uses Kerberos (krb5) whenever a credential cache, keytab, or non-NTLM integrated security connection is requested.
func configureUnixKerberosSQLServerAuth() {
	if runtime.GOOS == "windows" {
		return
	}
	ntlmProvider, _ := getRegisteredAuthProvider("ntlm")
	_ = integratedauth.SetIntegratedAuthenticationProvider("krb5", integratedauth.ProviderFunc(func(cfg msdsn.Config) (integratedauth.IntegratedAuthenticator, error) {
		normalizeKrb5Config(&cfg)
		return krb5.AuthProviderFunc.GetIntegratedAuthenticator(cfg)
	}))
	_ = integratedauth.SetIntegratedAuthenticationProvider("smart_unix", integratedauth.ProviderFunc(func(cfg msdsn.Config) (integratedauth.IntegratedAuthenticator, error) {
		// If DOMAIN\user + password is provided, use NTLM
		if strings.ContainsRune(cfg.User, '\\') && cfg.Password != "" && ntlmProvider != nil {
			return ntlmProvider.GetIntegratedAuthenticator(cfg)
		}
		normalizeKrb5Config(&cfg)
		return krb5.AuthProviderFunc.GetIntegratedAuthenticator(cfg)
	}))
	integratedauth.DefaultProviderName = "smart_unix"
}

func getRegisteredAuthProvider(name string) (integratedauth.Provider, error) {
	return integratedauth.ProviderFunc(func(cfg msdsn.Config) (integratedauth.IntegratedAuthenticator, error) {
		clone := cfg
		clone.Parameters = make(map[string]string, len(cfg.Parameters)+1)
		for k, v := range cfg.Parameters {
			clone.Parameters[k] = v
		}
		clone.Parameters["authenticator"] = name
		return integratedauth.GetIntegratedAuthenticator(clone)
	}), nil
}

// normalizeKrb5Config strips the MIT Kerberos "FILE:" prefix from KRB5CCNAME / krb5-credcachefile
// because go-mssqldb's krb5 package passes the path directly to os.Stat() and credentials.LoadCCache(),
// and qualifies short hostnames in ServerSPN (e.g., MSSQLSvc/T15P:1433 -> MSSQLSvc/T15P.RSYSLAB.COM:1433)
// so Active Directory KDCs can locate the registered SQL Server SPN.
func normalizeKrb5Config(cfg *msdsn.Config) {
	if cfg == nil {
		return
	}
	if cfg.Parameters == nil {
		cfg.Parameters = make(map[string]string)
	}
	cc := strings.TrimSpace(cfg.Parameters["krb5-credcachefile"])
	if cc == "" {
		cc = strings.TrimSpace(os.Getenv("KRB5CCNAME"))
	}
	if cleaned := stripKrb5FilePrefix(cc); cleaned != "" {
		cfg.Parameters["krb5-credcachefile"] = cleaned
	}
	kt := strings.TrimSpace(cfg.Parameters["krb5-keytabfile"])
	if kt == "" {
		kt = strings.TrimSpace(os.Getenv("KRB5_KTNAME"))
	}
	if cleaned := stripKrb5FilePrefix(kt); cleaned != "" {
		cfg.Parameters["krb5-keytabfile"] = cleaned
	}
	if strings.TrimSpace(cfg.Parameters["krb5-realm"]) == "" {
		if envRealm := strings.TrimSpace(os.Getenv("AD_REALM")); envRealm != "" {
			cfg.Parameters["krb5-realm"] = envRealm
		}
	}
	if cfg.Parameters["krb5-keytabfile"] != "" && strings.TrimSpace(cfg.User) == "" {
		if principal := resolveKerberosPrincipalUser(); principal != "" {
			cfg.User = principal
		}
	}
	cfg.ServerSPN = qualifyKerberosServerSPN(cfg.ServerSPN, cfg.Parameters["krb5-realm"], cfg.Parameters["krb5-configfile"])
}

func resolveKerberosPrincipalUser() string {
	for _, candidate := range []string{
		os.Getenv("KRB5_PRINCIPAL"),
		os.Getenv("AD_USER"),
		os.Getenv("FLOW_WINDOWS_USER"),
		os.Getenv("HOST_USERNAME"),
	} {
		v := strings.TrimSpace(candidate)
		if v == "" {
			continue
		}
		if slashIdx := strings.LastIndexAny(v, `\/`); slashIdx >= 0 {
			v = strings.TrimSpace(v[slashIdx+1:])
		}
		if atIdx := strings.IndexByte(v, '@'); atIdx > 0 {
			v = strings.TrimSpace(v[:atIdx])
		}
		if v != "" {
			return v
		}
	}
	return ""
}

func qualifyKerberosServerSPN(spn, explicitRealm, explicitConf string) string {
	spn = strings.TrimSpace(spn)
	if !strings.HasPrefix(spn, "MSSQLSvc/") {
		return spn
	}
	rest := spn[len("MSSQLSvc/"):]
	host := rest
	suffix := ""
	if colonIdx := strings.IndexByte(rest, ':'); colonIdx >= 0 {
		host = rest[:colonIdx]
		suffix = rest[colonIdx:]
	}
	if host == "" || strings.Contains(host, ".") {
		return spn
	}
	domain := strings.TrimSpace(explicitRealm)
	if domain == "" {
		domain = strings.TrimSpace(os.Getenv("AD_REALM"))
	}
	if domain == "" {
		domain = defaultRealmFromKrb5Conf(explicitConf)
	}
	if domain == "" || !strings.Contains(domain, ".") {
		return spn
	}
	return "MSSQLSvc/" + host + "." + domain + suffix
}

func defaultRealmFromKrb5Conf(confPath string) string {
	if strings.TrimSpace(confPath) == "" {
		confPath = strings.TrimSpace(os.Getenv("KRB5_CONFIG"))
	}
	if confPath == "" {
		confPath = "/etc/krb5.conf"
	}
	data, err := os.ReadFile(confPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if kv := strings.SplitN(trimmed, "=", 2); len(kv) == 2 && strings.EqualFold(strings.TrimSpace(kv[0]), "default_realm") {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

func stripKrb5FilePrefix(val string) string {
	v := strings.TrimSpace(val)
	if len(v) >= 5 && strings.EqualFold(v[:5], "FILE:") {
		return strings.TrimSpace(v[5:])
	}
	return v
}

func isOnPremADIntegratedDSN(dsn string) bool {
	lower := strings.ToLower(dsn)
	if !strings.Contains(lower, "activedirectoryintegrated") {
		return false
	}
	return !strings.Contains(lower, ".database.windows.net") && !strings.Contains(lower, ".Synapse.sql.azuresynapse.net")
}

func stripADIntegratedParams(dsn string) string {
	// For ADO-style connection strings (Data Source=...;Authentication=ActiveDirectoryIntegrated;...),
	// remove Authentication=ActiveDirectoryIntegrated and fedauth=ActiveDirectoryIntegrated so go-mssqldb uses SSPI/Kerberos.
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(dsn)), "sqlserver://") {
		parts := strings.Split(dsn, ";")
		kept := make([]string, 0, len(parts))
		for _, p := range parts {
			kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
			if len(kv) == 2 {
				k := strings.ToLower(strings.TrimSpace(kv[0]))
				v := strings.ToLower(strings.TrimSpace(kv[1]))
				if (k == "authentication" || k == "fedauth") && v == "activedirectoryintegrated" {
					continue
				}
			}
			if strings.TrimSpace(p) != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, ";")
	}
	return dsn
}

// ParseDSN extracts the sql.Open driver name, canonical dialect type, and clean DSN
// from any go-flow connection string format (including sql://driver@dsn, db://driver@dsn,
// Azure AD fedauth=, postgres://, mysql://, oracle://, sqlserver://, and sqlite files).
func ParseDSN(rawDSN string) (sqlDriver string, dialect string, cleanDSN string) {
	return ParseDSNWithDriver(rawDSN, "")
}

// ParseDSNWithDriver extracts the sql.Open driver name, canonical dialect type, and clean DSN,
// using explicitDriverHint when no scheme prefix is embedded in rawDSN.
func ParseDSNWithDriver(rawDSN, explicitDriverHint string) (sqlDriver string, dialect string, cleanDSN string) {
	dsn := strings.TrimSpace(rawDSN)
	driverHint := strings.ToLower(strings.TrimSpace(explicitDriverHint))

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
		if isOnPremADIntegratedDSN(dsn) {
			return "sqlserver", "sqlserver", stripADIntegratedParams(dsn)
		}
		return "azuresql", "sqlserver", dsn
	case "sqlserver", "mssql":
		if isOnPremADIntegratedDSN(dsn) {
			return "sqlserver", "sqlserver", stripADIntegratedParams(dsn)
		}
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
	case isOnPremADIntegratedDSN(dsn):
		return "sqlserver"
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
	return OpenDatabaseWithDriver(rawDSN, "")
}

// OpenDatabaseWithDriver opens a database connection using any supported go-flow DSN format and optional driver hint.
func OpenDatabaseWithDriver(rawDSN, driverHint string) (*sql.DB, string, error) {
	sqlDriver, dialect, cleanDSN := ParseDSNWithDriver(rawDSN, driverHint)
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
func FetchDatabaseSessionIdentity(ctx context.Context, querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, dialect string) (spid int64, dbUser string, appName string, err error) {
	defaultApp := defaultApplicationName()
	if querier == nil {
		return int64(os.Getpid()), "", defaultApp, nil
	}

	normDriver := strings.ToLower(strings.TrimSpace(dialect))
	querySession := func(query string) error {
		var pid sql.NullInt64
		var u, a sql.NullString
		e := querier.QueryRowContext(ctx, query).Scan(&pid, &u, &a)
		spid = pid.Int64
		dbUser = strings.TrimSpace(u.String)
		appName = strings.TrimSpace(a.String)
		return e
	}

	switch {
	case strings.Contains(normDriver, "sqlserver") || strings.Contains(normDriver, "mssql") || strings.Contains(normDriver, "azuresql") || strings.Contains(normDriver, "azuread"):
		err = querySession("SELECT CAST(@@SPID AS BIGINT), CAST(SYSTEM_USER AS NVARCHAR(256)), CAST(ISNULL(APP_NAME(), '') AS NVARCHAR(256))")
	case strings.Contains(normDriver, "postgres") || strings.Contains(normDriver, "pq") || strings.Contains(normDriver, "pgx"):
		err = querySession("SELECT CAST(pg_backend_pid() AS BIGINT), CAST(CURRENT_USER AS TEXT), COALESCE(current_setting('application_name', true), '')")
	case strings.Contains(normDriver, "mysql") || strings.Contains(normDriver, "mariadb"):
		err = querySession("SELECT CAST(CONNECTION_ID() AS UNSIGNED), CURRENT_USER(), COALESCE(@@session.program_name, '')")
		if err != nil {
			err = querySession("SELECT CAST(CONNECTION_ID() AS UNSIGNED), CURRENT_USER(), ''")
		}
	case strings.Contains(normDriver, "oracle") || strings.Contains(normDriver, "ora") || strings.Contains(normDriver, "godror"):
		err = querySession("SELECT TO_NUMBER(SYS_CONTEXT('USERENV', 'SID')), SYS_CONTEXT('USERENV', 'SESSION_USER'), COALESCE(SYS_CONTEXT('USERENV', 'MODULE'), SYS_CONTEXT('USERENV', 'CLIENT_PROGRAM_NAME'), '') FROM DUAL")
	case strings.Contains(normDriver, "sqlite"):
		userName, _ := runtimeIdentity()
		if userName == "" {
			userName = "sqlite"
		}
		return int64(os.Getpid()), userName, defaultApp, nil
	default:
		userName, _ := runtimeIdentity()
		return int64(os.Getpid()), userName, defaultApp, nil
	}

	if appName == "" {
		appName = defaultApp
	}
	if spid == 0 {
		spid = int64(os.Getpid())
	}
	return spid, dbUser, appName, err
}
