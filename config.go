package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// StartupConnectionString defines the primary database connection string loaded at startup.
// Supports all go-flow database drivers:
//   - Microsoft SQL Server: "sqlserver://sa:Password123!@localhost:1433?database=master&trustServerCertificate=true"
//   - Azure SQL (Entra ID): "sqlserver://server.database.windows.net?database=mydb&fedauth=ActiveDirectoryDefault"
//   - PostgreSQL:           "postgres://user:pass@localhost:5432/dbname?sslmode=disable"
//   - MySQL / MariaDB:      "mysql://user:pass@tcp(localhost:3306)/dbname?parseTime=true"
//   - SQLite:               "sql://sqlite@flow_api.db" or "flow_api.db"
//   - Oracle:               "oracle://user:pass@localhost:1521/service_name"
var StartupConnectionString = "sqlserver://db.example.com?database=EXAMPLE_DB&trustServerCertificate=true"

// ServerConfig holds the runtime configuration for go-flowAPI.
type ServerConfig struct {
	ConnectionString string
	ListenAddr       string
	DefaultSecureKey string
	KeyFilePath      string
	EnvFilePath      string
	LogFilePath      string
	ServiceName      string
	Debug            bool
	StrictACL        bool
	BootstrapUser    string
	BootstrapPass    string
	BootstrapPerm    string
	HashPassword     string
}

// LoadStartupConfig initializes ServerConfig starting from the StartupConnectionString variable,
// with optional overrides from an environment file (-env-file / FLOW_API_ENV_FILE), environment
// variables, and CLI flags.
func LoadStartupConfig(args []string) (*ServerConfig, error) {
	fs := flag.NewFlagSet("go-flowAPI", flag.ContinueOnError)

	dsnFlag := fs.String("dsn", "", "Database connection string override (defaults to StartupConnectionString / FLOW_API_DSN)")
	addrFlag := fs.String("addr", "", "HTTP listen address (default :8080 or FLOW_API_ADDR / PORT)")
	secureKeyFlag := fs.String("secure-key", "", "Default encryption/decryption key (falls back to FLOW_SECURE_KEY or SECURE_KEY)")
	keyFileFlag := fs.String("key-file", "", "Path to file containing default encryption/decryption key")
	envFileFlag := fs.String("env-file", "", "Optional path to KEY=VALUE environment configuration file (or FLOW_API_ENV_FILE)")
	logFileFlag := fs.String("log-file", "", "Optional file path to append server logs (or FLOW_API_LOG_FILE)")
	serviceNameFlag := fs.String("service-name", "GoFlowAPI", "Service name when running under the Windows Service Control Manager")
	debugFlag := fs.Bool("debug", false, "Enable verbose debug logging")
	strictACLFlag := fs.Bool("strict-acl", false, "Enforce strict ACL-only pipeline authorization for all non-admin users (or FLOW_API_STRICT_ACL)")
	bootstrapUserFlag := fs.String("bootstrap-user", "", "Initial API username to seed/upsert at startup (or FLOW_API_BOOTSTRAP_USER)")
	bootstrapPassFlag := fs.String("bootstrap-pass", "", "Password for -bootstrap-user (or FLOW_API_BOOTSTRAP_PASS)")
	bootstrapPermFlag := fs.String("bootstrap-perm", PermAll, "Permission role for -bootstrap-user (default: all)")
	hashPasswordFlag := fs.String("hash-password", "", "Generate and print a bcrypt hash for the given plaintext password, then exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	envFilePath := strings.TrimSpace(*envFileFlag)
	if envFilePath == "" {
		envFilePath = strings.TrimSpace(os.Getenv("FLOW_API_ENV_FILE"))
	}
	if envFilePath != "" {
		if err := loadEnvFile(envFilePath); err != nil {
			return nil, fmt.Errorf("failed loading env file %q: %w", envFilePath, err)
		}
	}

	dsn := strings.TrimSpace(StartupConnectionString)
	if envDSN := strings.TrimSpace(os.Getenv("FLOW_API_DSN")); envDSN != "" {
		dsn = envDSN
	}
	if strings.TrimSpace(*dsnFlag) != "" {
		dsn = strings.TrimSpace(*dsnFlag)
	}

	addr := ":8080"
	if envPort := strings.TrimSpace(os.Getenv("PORT")); envPort != "" {
		if _, err := strconv.Atoi(envPort); err == nil {
			addr = ":" + envPort
		} else {
			addr = envPort
		}
	}
	if envAddr := strings.TrimSpace(os.Getenv("FLOW_API_ADDR")); envAddr != "" {
		addr = envAddr
	}
	if strings.TrimSpace(*addrFlag) != "" {
		addr = strings.TrimSpace(*addrFlag)
	}

	logFilePath := strings.TrimSpace(*logFileFlag)
	if logFilePath == "" {
		logFilePath = strings.TrimSpace(os.Getenv("FLOW_API_LOG_FILE"))
	}

	serviceName := strings.TrimSpace(*serviceNameFlag)
	if serviceName == "" {
		serviceName = "GoFlowAPI"
	}

	debug := *debugFlag
	if !debug {
		if envDebug := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_API_DEBUG"))); envDebug == "true" || envDebug == "1" {
			debug = true
		}
	}

	strictACL := *strictACLFlag
	if !strictACL {
		if envStrict := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_API_STRICT_ACL"))); envStrict == "true" || envStrict == "1" {
			strictACL = true
		}
	}

	bootstrapUser := strings.TrimSpace(os.Getenv("FLOW_API_BOOTSTRAP_USER"))
	if strings.TrimSpace(*bootstrapUserFlag) != "" {
		bootstrapUser = strings.TrimSpace(*bootstrapUserFlag)
	}

	bootstrapPass := os.Getenv("FLOW_API_BOOTSTRAP_PASS")
	if *bootstrapPassFlag != "" {
		bootstrapPass = *bootstrapPassFlag
	}

	bootstrapPerm := strings.TrimSpace(os.Getenv("FLOW_API_BOOTSTRAP_PERM"))
	if strings.TrimSpace(*bootstrapPermFlag) != "" {
		bootstrapPerm = strings.TrimSpace(*bootstrapPermFlag)
	}
	if bootstrapPerm == "" {
		bootstrapPerm = PermAll
	}

	key := resolveKey(*secureKeyFlag, *keyFileFlag)

	return &ServerConfig{
		ConnectionString: dsn,
		ListenAddr:       addr,
		DefaultSecureKey: key,
		KeyFilePath:      strings.TrimSpace(*keyFileFlag),
		EnvFilePath:      envFilePath,
		LogFilePath:      logFilePath,
		ServiceName:      serviceName,
		Debug:            debug,
		StrictACL:        strictACL,
		BootstrapUser:    bootstrapUser,
		BootstrapPass:    bootstrapPass,
		BootstrapPerm:    bootstrapPerm,
		HashPassword:     *hashPasswordFlag,
	}, nil
}

// loadEnvFile parses a KEY=VALUE configuration file (supporting '#', ';' comments,
// optional 'export ' prefixes, and single/double quotes while preserving literal
// backslashes for Windows paths and DOMAIN\user identities) and populates unset
// environment variables.
func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		eqIdx := strings.IndexByte(line, '=')
		if eqIdx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eqIdx])
		val := strings.TrimSpace(line[eqIdx+1:])
		if key == "" {
			continue
		}
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		} else if hashIdx := strings.Index(val, " #"); hashIdx >= 0 {
			val = strings.TrimSpace(val[:hashIdx])
		}
		if strings.TrimSpace(os.Getenv(key)) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return nil
}

func resolveKey(cliKey, keyFilePath string) string {
	if strings.TrimSpace(cliKey) != "" {
		return strings.TrimSpace(cliKey)
	}
	if strings.TrimSpace(keyFilePath) != "" {
		if data, err := os.ReadFile(strings.TrimSpace(keyFilePath)); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	if envKey := strings.TrimSpace(os.Getenv("FLOW_SECURE_KEY")); envKey != "" {
		return envKey
	}
	if envKey := strings.TrimSpace(os.Getenv("SECURE_KEY")); envKey != "" {
		return envKey
	}
	return ""
}
