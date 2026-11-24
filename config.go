package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/etl-madness/flow"
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
	DataDir          string
	ServiceName      string
	Debug            bool
	StrictACL        bool
	DisableStrictACL bool
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
	dataDirFlag := fs.String("data-dir", "", "Optional root directory for per-job <user_login>/<pid>-<run_id> sandboxed workspaces (or FLOW_API_DATA_DIR)")
	serviceNameFlag := fs.String("service-name", "GoFlowAPI", "Service name when running under the Windows Service Control Manager")
	debugFlag := fs.Bool("debug", false, "Enable verbose debug logging")
	strictACLFlag := fs.Bool("strict-acl", true, "Enforce strict ACL-only pipeline authorization for all non-admin users so accounts without ACLs have no pipeline access (default: true; set -strict-acl=false or FLOW_API_STRICT_ACL=false for legacy global pipeline roles)")
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

	dataDir := strings.TrimSpace(*dataDirFlag)
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("FLOW_API_DATA_DIR"))
	}
	if dataDir != "" {
		absDataDir, absErr := filepath.Abs(dataDir)
		if absErr != nil {
			return nil, fmt.Errorf("invalid -data-dir %q: %w", dataDir, absErr)
		}
		if mkErr := os.MkdirAll(absDataDir, 0750); mkErr != nil {
			return nil, fmt.Errorf("failed creating -data-dir %q: %w", absDataDir, mkErr)
		}
		dataDir = filepath.Clean(absDataDir)
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

	strictFlagExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "strict-acl" {
			strictFlagExplicit = true
		}
	})
	strictACL := *strictACLFlag
	if !strictFlagExplicit {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_API_STRICT_ACL"))) {
		case "false", "0", "no", "off":
			strictACL = false
		case "true", "1", "yes", "on":
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
		DataDir:          dataDir,
		ServiceName:      serviceName,
		Debug:            debug,
		StrictACL:        strictACL,
		DisableStrictACL: !strictACL,
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
		if data, err := readSafeLocalFile(keyFilePath); err == nil {
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

const safeLocalPathAlphabet = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

func sanitizeRelativeLocalPath(rel string) (string, bool) {
	if rel == "" || rel == "." || strings.Contains(rel, "..") || !filepath.IsLocal(rel) {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(rel))
	for i := 0; i < len(rel); i++ {
		idx := strings.IndexByte(safeLocalPathAlphabet, rel[i])
		if idx < 0 {
			return "", false
		}
		b.WriteByte(safeLocalPathAlphabet[idx])
	}
	return b.String(), true
}

func allowedLocalBaseDirs() []string {
	var dirs []string
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			dirs = append(dirs, filepath.Clean(abs))
		}
	}
	if tmp := os.TempDir(); tmp != "" {
		if abs, err := filepath.Abs(tmp); err == nil {
			dirs = append(dirs, filepath.Clean(abs))
		}
	}
	return dirs
}

func resolveSafeLocalPath(rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || strings.ContainsRune(trimmed, 0) || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("invalid or unsafe file path")
	}
	absPath, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", err
	}
	for _, baseDir := range allowedLocalBaseDirs() {
		rel, relErr := filepath.Rel(baseDir, absPath)
		if relErr != nil {
			continue
		}
		if cleanRel, ok := sanitizeRelativeLocalPath(rel); ok {
			safePath := filepath.Join(baseDir, cleanRel)
			if !strings.Contains(safePath, "..") && strings.HasPrefix(safePath, baseDir) {
				return safePath, nil
			}
		}
	}
	return "", fmt.Errorf("file path %q is outside permitted directories", rawPath)
}

func readSafeLocalFile(rawPath string) ([]byte, error) {
	safePath, err := resolveSafeLocalPath(rawPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(safePath)
}

func writeSafeLocalFile(rawPath string, data []byte, perm os.FileMode) error {
	safePath, err := resolveSafeLocalPath(rawPath)
	if err != nil {
		return err
	}
	return os.WriteFile(safePath, data, perm)
}

// JobWorkspace represents an isolated per-job directory under -data-dir:
// <data-dir>/<sanitized_user_login>/<pid>-<run_id>.
type JobWorkspace struct {
	RootDir string
	UserDir string
	Dir     string
	RunID   string
}

// sanitizeUserDirName converts a user login (e.g. "RSYSLAB\\SVC_FLOW_API",
// "U00001@RSYSLAB.COM", "admin") into a safe single path segment.
func sanitizeUserDirName(userLogin string) string {
	trimmed := strings.TrimSpace(strings.ToLower(userLogin))
	if trimmed == "" {
		return "anonymous"
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	lastUnderscore := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			b.WriteByte(ch)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	cleaned := strings.Trim(b.String(), "_-")
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "anonymous"
	}
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	return cleaned
}

// NewJobWorkspace creates <data-dir>/<user_login>/<pid>-<run_id> for the duration of a job.
func NewJobWorkspace(dataDir, userLogin, runID string) (*JobWorkspace, error) {
	trimmedRoot := strings.TrimSpace(dataDir)
	if trimmedRoot == "" {
		return nil, nil
	}
	absRoot, err := filepath.Abs(trimmedRoot)
	if err != nil {
		return nil, fmt.Errorf("failed resolving data-dir %q: %w", dataDir, err)
	}
	absRoot = filepath.Clean(absRoot)

	safeUser := sanitizeUserDirName(userLogin)
	safeRunID := sanitizeUserDirName(runID)
	if safeRunID == "" || safeRunID == "anonymous" {
		safeRunID = generateID("run")
	}
	userDir := filepath.Join(absRoot, safeUser)
	jobDir := filepath.Join(userDir, fmt.Sprintf("%d-%s", os.Getpid(), safeRunID))

	if err := os.MkdirAll(jobDir, 0750); err != nil {
		return nil, fmt.Errorf("failed creating job workspace %q: %w", jobDir, err)
	}
	return &JobWorkspace{
		RootDir: absRoot,
		UserDir: userDir,
		Dir:     jobDir,
		RunID:   runID,
	}, nil
}

// ResolvePath resolves rawPath strictly inside the job workspace directory.
func (w *JobWorkspace) ResolvePath(rawPath string) (string, error) {
	if w == nil || w.Dir == "" {
		return resolveSafeLocalPath(rawPath)
	}
	return flow.ResolveConfinedPath(w.Dir, rawPath)
}

// ReadFile reads a file strictly inside the job workspace directory.
func (w *JobWorkspace) ReadFile(rawPath string) ([]byte, error) {
	p, err := w.ResolvePath(rawPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// WriteFile writes a file (creating subdirectories as needed) strictly inside the job workspace directory.
func (w *JobWorkspace) WriteFile(rawPath string, data []byte, perm os.FileMode) error {
	p, err := w.ResolvePath(rawPath)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(p); dir != "" && dir != "." {
		if mkErr := os.MkdirAll(dir, 0750); mkErr != nil {
			return mkErr
		}
	}
	return os.WriteFile(p, data, perm)
}

// Cleanup removes the per-job workspace directory when the job finishes.
func (w *JobWorkspace) Cleanup() {
	if w == nil || w.Dir == "" {
		return
	}
	_ = os.RemoveAll(w.Dir)
}
