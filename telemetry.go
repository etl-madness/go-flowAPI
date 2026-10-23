package main

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/user"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/etl-madness/flow"
)

const (
	maxNodeOutputBytes         = 65536 // 64 KB safety cap for node_output
	nodeOutputTruncationSuffix = "\n...[TRUNCATED]"
)

var sensitiveOutputPattern = regexp.MustCompile(`(?i)(password|pwd|token|secret|authorization)\s*([=:])\s*([^\s,;]+)`)

func truncateNodeOutput(output string, maxLen int) string {
	if maxLen <= 0 || len(output) <= maxLen {
		return output
	}
	suffixLen := len(nodeOutputTruncationSuffix)
	if maxLen <= suffixLen {
		cut := maxLen
		for cut > 0 && !utf8.RuneStart(output[cut]) {
			cut--
		}
		return output[:cut]
	}
	cut := maxLen - suffixLen
	for cut > 0 && !utf8.RuneStart(output[cut]) {
		cut--
	}
	return output[:cut] + nodeOutputTruncationSuffix
}

func redactNodeOutput(output string) string {
	if output == "" {
		return ""
	}
	redacted := sensitiveOutputPattern.ReplaceAllString(output, "$1$2[REDACTED]")
	return truncateNodeOutput(redacted, maxNodeOutputBytes)
}

func generateID(prefix string) string {
	bytes := make([]byte, 16)
	if _, err := cryptorand.Read(bytes); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func runtimeIdentity() (string, string) {
	userName := ""
	if currentUser, err := user.Current(); err == nil && currentUser != nil {
		userName = currentUser.Username
	}
	if userName == "" {
		userName = os.Getenv("USER")
	}
	if userName == "" {
		userName = os.Getenv("USERNAME")
	}

	hostname, _ := os.Hostname()
	if hostname != "" && !strings.Contains(hostname, ".") {
		if cname, err := net.LookupCNAME(hostname); err == nil && cname != "" {
			hostname = strings.TrimSuffix(cname, ".")
		}
	}
	if hostname == "" {
		hostname = os.Getenv("HOSTNAME")
	}
	if hostname == "" {
		hostname = os.Getenv("COMPUTERNAME")
	}
	return userName, hostname
}

// =============================================================================
// 1. API Event Audit Logging (TableAPIAuditEvents)
// =============================================================================

// APIAuditEvent represents a single API operation logged to TableAPIAuditEvents.
type APIAuditEvent struct {
	EventID      string    `json:"event_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	EventType    string    `json:"event_type"`
	HTTPMethod   string    `json:"http_method"`
	Path         string    `json:"path"`
	ArtifactType string    `json:"artifact_type,omitempty"`
	ArtifactName string    `json:"artifact_name,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	AuthUser     string    `json:"auth_user,omitempty"`
	RemoteAddr   string    `json:"remote_addr,omitempty"`
	StatusCode   int       `json:"status_code"`
	Status       string    `json:"status"`
	DurationMs   int64     `json:"duration_ms"`
	ErrorMessage string    `json:"error_message,omitempty"`
	DetailsJSON  string    `json:"details_json,omitempty"`
}

var defaultAPIAuditCols = []string{
	"event_id", "occurred_at", "event_type", "http_method", "path",
	"artifact_type", "artifact_name", "run_id", "auth_user", "remote_addr",
	"status_code", "status", "duration_ms", "error_message", "details_json",
}

func (s *APIServer) logAPIEvent(ctx context.Context, evt APIAuditEvent) {
	if s == nil || s.db == nil {
		return
	}
	if evt.EventID == "" {
		evt.EventID = generateID("api")
	}
	if evt.OccurredAt.IsZero() {
		evt.OccurredAt = time.Now().UTC()
	}

	query := BuildInsertQuery(s.dialect, TableAPIAuditEvents, defaultAPIAuditCols)
	_, _ = s.db.ExecContext(
		ctx,
		query,
		evt.EventID,
		evt.OccurredAt.UTC(),
		evt.EventType,
		evt.HTTPMethod,
		evt.Path,
		evt.ArtifactType,
		evt.ArtifactName,
		evt.RunID,
		evt.AuthUser,
		evt.RemoteAddr,
		evt.StatusCode,
		evt.Status,
		evt.DurationMs,
		evt.ErrorMessage,
		evt.DetailsJSON,
	)
}

// QueryAPIAuditEvents queries TableAPIAuditEvents with optional filters.
func (s *APIServer) QueryAPIAuditEvents(ctx context.Context, eventType, authUser, status string, limit int) ([]APIAuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	table := resolveTableName(s.dialect, TableAPIAuditEvents)

	var clauses []string
	var args []any
	argIdx := 1

	if strings.TrimSpace(eventType) != "" {
		clauses = append(clauses, fmt.Sprintf("event_type = %s", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(eventType))
		argIdx++
	}
	if strings.TrimSpace(authUser) != "" {
		clauses = append(clauses, fmt.Sprintf("auth_user = %s", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(authUser))
		argIdx++
	}
	if strings.TrimSpace(status) != "" {
		clauses = append(clauses, fmt.Sprintf("status = %s", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(status))
		argIdx++
	}

	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(
		"SELECT event_id, occurred_at, event_type, COALESCE(http_method, ''), COALESCE(path, ''), COALESCE(artifact_type, ''), COALESCE(artifact_name, ''), COALESCE(run_id, ''), COALESCE(auth_user, ''), COALESCE(remote_addr, ''), COALESCE(status_code, 0), COALESCE(status, ''), COALESCE(duration_ms, 0), COALESCE(error_message, ''), COALESCE(details_json, '') FROM %s%s ORDER BY occurred_at DESC",
		table, whereSQL,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []APIAuditEvent
	for rows.Next() && len(events) < limit {
		var e APIAuditEvent
		var occurredRaw any
		if err := rows.Scan(
			&e.EventID,
			&occurredRaw,
			&e.EventType,
			&e.HTTPMethod,
			&e.Path,
			&e.ArtifactType,
			&e.ArtifactName,
			&e.RunID,
			&e.AuthUser,
			&e.RemoteAddr,
			&e.StatusCode,
			&e.Status,
			&e.DurationMs,
			&e.ErrorMessage,
			&e.DetailsJSON,
		); err != nil {
			return nil, err
		}
		e.OccurredAt = parseDBTime(occurredRaw)
		events = append(events, e)
	}
	if events == nil {
		events = []APIAuditEvent{}
	}
	return events, rows.Err()
}

// =============================================================================
// 2. Pipeline Execution Telemetry Sink (TablePipelineEvents & TablePipelineRuns)
// =============================================================================

// NodeDBTarget describes the database connection targets for a pipeline node.
type NodeDBTarget struct {
	NodeKind string
	DBName   string
	TargetDB string
}

// DatabaseSessionIdentity captures the session ID, db user, and application name for a connection.
type DatabaseSessionIdentity struct {
	SPID            int64
	DBUser          string
	ApplicationName string
}

type finishedNodeEvent struct {
	ExecutionID string
	NodeID      string
	NodePath    string
	Sequence    uint64
}

// DatabaseSink implements flow.EventSink and writes pipeline execution telemetry
// to TablePipelineEvents, matching go-flow's DatabaseSink capabilities.
type DatabaseSink struct {
	DB               *sql.DB
	Driver           string
	insertQuery      string
	configuredCols   []string
	colsDetected     bool
	isNumericSPID    bool
	logNodeOutput    bool
	hasNodeOutputCol bool
	nodeOutputMaxLen int
	spid             int64
	dbUser           string
	appName          string
	mu               sync.RWMutex
	runID            string
	status           string
	errorClass       string
	errorMsg         string
	debug            bool
	registry         *flow.Registry
	nodeTargets      map[string]NodeDBTarget
	cachedIdentities map[string]DatabaseSessionIdentity
	finishedNodes    []finishedNodeEvent
}

var defaultEventCols = []string{
	"run_id", "execution_id", "sequence_num", "occurred_at", "event_type",
	"node_kind", "node_id", "status", "user_name", "hostname", "options_path",
	"error_message", "rows_read", "rows_written", "rows_affected",
}

var knownEventCols = map[string]bool{
	"run_id":             true,
	"execution_id":       true,
	"sequence_num":       true,
	"occurred_at":        true,
	"event_type":         true,
	"node_kind":          true,
	"node_id":            true,
	"status":             true,
	"user_name":          true,
	"os_user_name":       true,
	"db_user_name":       true,
	"database_user_name": true,
	"spid":               true,
	"sspid":              true,
	"application_name":   true,
	"app_name":           true,
	"hostname":           true,
	"options_path":       true,
	"error_message":      true,
	"rows_read":          true,
	"rows_written":       true,
	"rows_affected":      true,
	"node_output":        true,
}

func NewDatabaseSink(db *sql.DB, dialect string, logNodeOutput bool, debug bool) *DatabaseSink {
	sink := &DatabaseSink{
		DB:            db,
		Driver:        dialect,
		logNodeOutput: logNodeOutput,
		debug:         debug,
		spid:          int64(os.Getpid()),
		appName:       defaultApplicationName(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if db != nil {
		if spid, dbUser, appName, err := FetchDatabaseSessionIdentity(ctx, db, dialect); err == nil {
			sink.spid = spid
			sink.dbUser = dbUser
			sink.appName = appName
		}
		sink.detectColumns(ctx)
	} else {
		sink.configuredCols = defaultEventCols
		sink.insertQuery = BuildInsertQuery(dialect, TablePipelineEvents, defaultEventCols)
	}
	return sink
}

func (s *DatabaseSink) detectColumns(ctx context.Context) {
	if s.DB == nil {
		s.configuredCols = defaultEventCols
		s.insertQuery = BuildInsertQuery(s.Driver, TablePipelineEvents, defaultEventCols)
		return
	}

	table := resolveTableName(s.Driver, TablePipelineEvents)
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s WHERE 1=0", table))
	if err == nil {
		colTypes, typeErr := rows.ColumnTypes()
		colNames, colErr := rows.Columns()
		rows.Close()
		if colErr == nil && len(colNames) > 0 {
			var available []string
			s.hasNodeOutputCol = false
			for _, c := range colNames {
				lower := strings.ToLower(strings.TrimSpace(c))
				if lower == "node_output" {
					s.hasNodeOutputCol = true
					if !s.logNodeOutput {
						continue
					}
				}
				if knownEventCols[lower] {
					available = append(available, lower)
				}
			}
			if len(available) == 0 {
				s.configuredCols = defaultEventCols
			} else {
				s.configuredCols = available
			}
			s.insertQuery = BuildInsertQuery(s.Driver, TablePipelineEvents, s.configuredCols)
			s.colsDetected = true

			if typeErr == nil && len(colTypes) > 0 {
				for _, ct := range colTypes {
					name := strings.ToLower(ct.Name())
					if name == "spid" || name == "sspid" {
						typeName := strings.ToUpper(ct.DatabaseTypeName())
						if (strings.Contains(typeName, "INT") || strings.Contains(typeName, "NUM") || strings.Contains(typeName, "DEC")) &&
							!strings.Contains(typeName, "CHAR") && !strings.Contains(typeName, "TEXT") {
							s.isNumericSPID = true
						}
					} else if name == "node_output" {
						if colLen, ok := ct.Length(); ok && colLen > 0 && colLen < int64(maxNodeOutputBytes) {
							s.nodeOutputMaxLen = int(colLen)
						}
					}
				}
			}
			return
		}
	}

	s.configuredCols = defaultEventCols
	s.insertQuery = BuildInsertQuery(s.Driver, TablePipelineEvents, defaultEventCols)
}

// SPID returns the session ID or process ID captured for the database connection.
func (s *DatabaseSink) SPID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.spid
}

// SSPID returns the session ID or process ID captured for the database connection.
func (s *DatabaseSink) SSPID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.spid
}

// DBUser returns the database user name captured for the database connection.
func (s *DatabaseSink) DBUser() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbUser
}

// ApplicationName returns the application name captured for the database connection.
func (s *DatabaseSink) ApplicationName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appName
}

// SessionIdentity returns SPID, DB User, and Application Name.
func (s *DatabaseSink) SessionIdentity() (int64, string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.spid, s.dbUser, s.appName
}

// SetSessionIdentity explicitly overrides the session identity for testing or connection pooling.
func (s *DatabaseSink) SetSessionIdentity(spid int64, dbUser string, appName ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spid = spid
	s.dbUser = dbUser
	if len(appName) > 0 && appName[0] != "" {
		s.appName = appName[0]
	} else if s.appName == "" {
		s.appName = defaultApplicationName()
	}
}

// SetRegistry sets the flow.Registry used to look up active database connection pools.
func (s *DatabaseSink) SetRegistry(reg *flow.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry = reg
}

// SetDBSessionIdentity sets a known session identity for a specific database name.
func (s *DatabaseSink) SetDBSessionIdentity(dbName string, spid int64, dbUser string, appName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedIdentities == nil {
		s.cachedIdentities = make(map[string]DatabaseSessionIdentity)
	}
	s.cachedIdentities[dbName] = DatabaseSessionIdentity{
		SPID:            spid,
		DBUser:          dbUser,
		ApplicationName: appName,
	}
}

// RegisterNodeTarget registers the DB targets for a given node ID or node path.
func (s *DatabaseSink) RegisterNodeTarget(nodeIDOrPath string, target NodeDBTarget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodeTargets == nil {
		s.nodeTargets = make(map[string]NodeDBTarget)
	}
	s.nodeTargets[nodeIDOrPath] = target
}

// RegisterNodes registers the AST nodes to map execution events to actual database connections.
func (s *DatabaseSink) RegisterNodes(nodes []flow.PipelineNode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodeTargets == nil {
		s.nodeTargets = make(map[string]NodeDBTarget)
	}
	s.registerNodesRecursive(nodes, "")
}

func (s *DatabaseSink) registerNodesRecursive(nodes []flow.PipelineNode, parentPath string) {
	for index, node := range nodes {
		kind, id := nodeIdentityFromAST(node)
		path := fmt.Sprintf("%s[%d]", kind, index)
		if parentPath != "" {
			path = fmt.Sprintf("%s/%s", parentPath, path)
		}

		var target NodeDBTarget
		hasTarget := false

		switch node.Kind {
		case flow.NodeSQL:
			if node.SQL != nil {
				target = NodeDBTarget{
					NodeKind: "sql",
					DBName:   node.SQL.DBName,
				}
				hasTarget = true
			}
		case flow.NodeSQLBulk:
			if node.SQLBulk != nil {
				targetDB := node.SQLBulk.TargetDB
				if targetDB == "" {
					targetDB = node.SQLBulk.DBName
				}
				target = NodeDBTarget{
					NodeKind: "sql_bulk",
					DBName:   node.SQLBulk.DBName,
					TargetDB: targetDB,
				}
				hasTarget = true
			}
		}

		if hasTarget {
			s.nodeTargets[path] = target
			if id != "" {
				s.nodeTargets[id] = target
			}
		}

		if len(node.Children) > 0 {
			s.registerNodesRecursive(node.Children, path)
		}
		if len(node.ElseNodes) > 0 {
			s.registerNodesRecursive(node.ElseNodes, path)
		}
	}
}

func nodeIdentityFromAST(node flow.PipelineNode) (string, string) {
	switch node.Kind {
	case flow.NodeKV:
		if node.KV != nil {
			return "kv", node.KV.ID
		}
		return "kv", ""
	case flow.NodeKVBulk:
		if node.KVBulk != nil {
			return "kv_bulk", node.KVBulk.ID
		}
		return "kv_bulk", ""
	case flow.NodeAssert:
		if node.Assert != nil {
			return "assert", node.Assert.ID
		}
		return "assert", ""
	case flow.NodeSQL:
		if node.SQL != nil {
			return "sql", node.SQL.ID
		}
		return "sql", ""
	case flow.NodeSQLBulk:
		if node.SQLBulk != nil {
			return "sql_bulk", node.SQLBulk.ID
		}
		return "sql_bulk", ""
	case flow.NodeScript:
		if node.Script != nil {
			return "script", node.Script.ID
		}
		return "script", ""
	case flow.NodeHTTPClient:
		if node.HTTPClient != nil {
			return "http_client", node.HTTPClient.ID
		}
		return "http_client", ""
	case flow.NodeTemplate:
		if node.Template != nil {
			return "template", node.Template.ID
		}
		return "template", ""
	case flow.NodeHtmlTemplate:
		if node.HtmlTemplate != nil {
			return "template_html", node.HtmlTemplate.ID
		}
		return "template_html", ""
	case flow.NodeFileSave:
		if node.FileSave != nil {
			return "file_save", node.FileSave.ID
		}
		return "file_save", ""
	case flow.NodeFileRead:
		if node.FileRead != nil {
			return "file_read", node.FileRead.ID
		}
		return "file_read", ""
	case flow.NodeExcelRead:
		if node.ExcelRead != nil {
			return "excel_read", node.ExcelRead.ID
		}
		return "excel_read", ""
	case flow.NodeExcelWrite:
		if node.ExcelWrite != nil {
			return "excel_write", node.ExcelWrite.ID
		}
		return "excel_write", ""
	case flow.NodeXMLXPath:
		if node.XmlXPath != nil {
			return "xml_xpath", node.XmlXPath.ID
		}
		return "xml_xpath", ""
	case flow.NodeJSONPath:
		if node.JsonPath != nil {
			return "json_path", node.JsonPath.ID
		}
		return "json_path", ""
	case flow.NodeYAMLPath:
		if node.YamlPath != nil {
			return "yaml_path", node.YamlPath.ID
		}
		return "yaml_path", ""
	case flow.NodeGroup:
		return "group", node.GroupID
	case flow.NodeParallel:
		return "parallel", node.GroupID
	case flow.NodeIf:
		return "if", node.GroupID
	case flow.NodeForEach:
		return "foreach", node.GroupID
	case flow.NodeWhile:
		return "while", node.GroupID
	default:
		return "unknown", ""
	}
}

func (s *DatabaseSink) resolveDBName(name string) string {
	name = strings.TrimSpace(name)
	if !strings.Contains(name, "{{") {
		return name
	}
	s.mu.RLock()
	reg := s.registry
	s.mu.RUnlock()
	if reg == nil {
		return name
	}
	vars := reg.CopyVariables()
	for k, v := range vars {
		placeholder := fmt.Sprintf("{{%s}}", k)
		name = strings.ReplaceAll(name, placeholder, fmt.Sprintf("%v", v))
	}
	return strings.TrimSpace(name)
}

func (s *DatabaseSink) getOrFetchSessionIdentity(ctx context.Context, dbName string) (DatabaseSessionIdentity, error) {
	s.mu.RLock()
	if s.cachedIdentities != nil {
		if ident, ok := s.cachedIdentities[dbName]; ok {
			s.mu.RUnlock()
			return ident, nil
		}
	}
	defaultSPID := s.spid
	defaultDBUser := s.dbUser
	defaultAppName := s.appName
	reg := s.registry
	s.mu.RUnlock()

	if dbName == "" || strings.EqualFold(dbName, "log_db") || strings.EqualFold(dbName, "logg_db") {
		return DatabaseSessionIdentity{
			SPID:            defaultSPID,
			DBUser:          defaultDBUser,
			ApplicationName: defaultAppName,
		}, nil
	}

	if reg == nil {
		return DatabaseSessionIdentity{}, fmt.Errorf("registry not set")
	}

	handle, err := reg.GetDBHandle(dbName)
	if err != nil {
		return DatabaseSessionIdentity{}, err
	}

	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()

	spid, dbUser, appName, err := FetchDatabaseSessionIdentity(fetchCtx, handle.Conn, handle.Driver)
	if err != nil {
		return DatabaseSessionIdentity{}, err
	}

	ident := DatabaseSessionIdentity{
		SPID:            spid,
		DBUser:          dbUser,
		ApplicationName: appName,
	}

	s.mu.Lock()
	if s.cachedIdentities == nil {
		s.cachedIdentities = make(map[string]DatabaseSessionIdentity)
	}
	s.cachedIdentities[dbName] = ident
	s.mu.Unlock()

	return ident, nil
}

func (s *DatabaseSink) resolveEventIdentity(ctx context.Context, event flow.ExecutionEvent) (spid any, srcSPIDInt int64, appName string, dbUser string) {
	s.mu.RLock()
	defaultSPID := s.spid
	defaultDBUser := s.dbUser
	defaultAppName := s.appName
	var target NodeDBTarget
	var found bool
	if s.nodeTargets != nil {
		if event.NodePath != "" {
			target, found = s.nodeTargets[event.NodePath]
		}
		if !found && event.NodeID != "" {
			target, found = s.nodeTargets[event.NodeID]
		}
	}
	s.mu.RUnlock()

	if !found || (event.NodeKind != "" && event.NodeKind != "sql" && event.NodeKind != "sql_bulk") {
		return defaultSPID, defaultSPID, defaultAppName, defaultDBUser
	}

	if target.NodeKind == "sql_bulk" {
		srcDB := s.resolveDBName(target.DBName)
		tgtDB := s.resolveDBName(target.TargetDB)
		if tgtDB == "" {
			tgtDB = srcDB
		}

		if srcDB != "" && tgtDB != "" && srcDB != tgtDB {
			srcIdent, srcErr := s.getOrFetchSessionIdentity(ctx, srcDB)
			tgtIdent, tgtErr := s.getOrFetchSessionIdentity(ctx, tgtDB)
			if srcErr == nil && tgtErr == nil {
				return fmt.Sprintf("%d->%d", srcIdent.SPID, tgtIdent.SPID),
					srcIdent.SPID,
					tgtIdent.ApplicationName,
					fmt.Sprintf("%s->%s", srcIdent.DBUser, tgtIdent.DBUser)
			}
			if tgtErr == nil {
				return tgtIdent.SPID, tgtIdent.SPID, tgtIdent.ApplicationName, tgtIdent.DBUser
			}
		} else if srcDB != "" {
			ident, err := s.getOrFetchSessionIdentity(ctx, srcDB)
			if err == nil {
				return ident.SPID, ident.SPID, ident.ApplicationName, ident.DBUser
			}
		}
		return defaultSPID, defaultSPID, defaultAppName, defaultDBUser
	}

	if target.NodeKind == "sql" {
		dbName := s.resolveDBName(target.DBName)
		if dbName != "" {
			ident, err := s.getOrFetchSessionIdentity(ctx, dbName)
			if err == nil {
				return ident.SPID, ident.SPID, ident.ApplicationName, ident.DBUser
			}
		}
	}

	return defaultSPID, defaultSPID, defaultAppName, defaultDBUser
}

func (s *DatabaseSink) Emit(ctx context.Context, event flow.ExecutionEvent) error {
	s.mu.Lock()
	if event.RunID != "" {
		if s.runID != "" && s.runID != event.RunID {
			s.finishedNodes = nil
		}
		s.runID = event.RunID
	}
	if event.Type == flow.EventRunFinished {
		s.status = string(event.Status)
		s.errorClass = string(event.ErrorClass)
		s.errorMsg = event.ErrorMessage
	}
	if event.Type == flow.EventNodeFinished {
		s.finishedNodes = append(s.finishedNodes, finishedNodeEvent{
			ExecutionID: event.ExecutionID,
			NodeID:      event.NodeID,
			NodePath:    event.NodePath,
			Sequence:    event.Sequence,
		})
	}
	if !s.colsDetected && s.DB != nil {
		s.detectColumns(ctx)
	}
	cols := s.configuredCols
	query := s.insertQuery
	isNumericSPID := s.isNumericSPID
	s.mu.Unlock()

	if s.DB == nil {
		return nil
	}

	spid, srcSPIDInt, appName, dbUser := s.resolveEventIdentity(ctx, event)

	if query == "" || len(cols) == 0 {
		cols = defaultEventCols
		query = BuildInsertQuery(s.Driver, TablePipelineEvents, cols)
	}

	osUser := event.UserName
	if osUser == "" {
		osUser, _ = runtimeIdentity()
	}

	args := make([]any, 0, len(cols))
	for _, col := range cols {
		switch col {
		case "run_id":
			args = append(args, event.RunID)
		case "execution_id":
			args = append(args, event.ExecutionID)
		case "sequence_num":
			args = append(args, event.Sequence)
		case "occurred_at":
			args = append(args, event.OccurredAt.UTC())
		case "event_type":
			args = append(args, string(event.Type))
		case "node_kind":
			args = append(args, event.NodeKind)
		case "node_id":
			args = append(args, event.NodeID)
		case "status":
			args = append(args, string(event.Status))
		case "user_name", "os_user_name":
			args = append(args, osUser)
		case "db_user_name", "database_user_name":
			args = append(args, dbUser)
		case "spid", "sspid":
			if isNumericSPID && s.Driver != "sqlite" && s.Driver != "sqlite3" {
				args = append(args, srcSPIDInt)
			} else {
				args = append(args, spid)
			}
		case "application_name", "app_name":
			args = append(args, appName)
		case "hostname":
			args = append(args, event.Hostname)
		case "options_path":
			args = append(args, event.OptionsPath)
		case "error_message":
			args = append(args, event.ErrorMessage)
		case "rows_read":
			args = append(args, event.RowCounts.Read)
		case "rows_written":
			args = append(args, event.RowCounts.Written)
		case "rows_affected":
			args = append(args, event.RowCounts.Affected)
		case "node_output":
			args = append(args, nil)
		default:
			args = append(args, nil)
		}
	}

	emitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.DB.ExecContext(emitCtx, query, args...)
	return err
}

func (s *DatabaseSink) RunDetails() (runID, status, errorClass, errorMsg string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runID, s.status, s.errorClass, s.errorMsg
}

func buildUpdateNodeFinishedOutputQuery(dialect, tableConst string) string {
	table := resolveTableName(dialect, tableConst)
	switch strings.ToLower(dialect) {
	case "postgres":
		return fmt.Sprintf("UPDATE %s SET node_output = $1 WHERE run_id = $2 AND execution_id = $3 AND event_type = 'node.finished'", table)
	case "sqlserver", "azuresql":
		return fmt.Sprintf("UPDATE %s SET node_output = @p1 WHERE run_id = @p2 AND execution_id = @p3 AND event_type = 'node.finished'", table)
	case "oracle":
		return fmt.Sprintf("UPDATE %s SET node_output = :1 WHERE run_id = :2 AND execution_id = :3 AND event_type = 'node.finished'", table)
	default:
		return fmt.Sprintf("UPDATE %s SET node_output = ? WHERE run_id = ? AND execution_id = ? AND event_type = 'node.finished'", table)
	}
}

func (s *DatabaseSink) RecordNodeOutputs(ctx context.Context, results []flow.ScriptResult) error {
	if s == nil || s.DB == nil || !s.logNodeOutput || len(results) == 0 {
		return nil
	}

	s.mu.Lock()
	if !s.colsDetected {
		s.detectColumns(ctx)
	}
	hasCol := s.hasNodeOutputCol
	maxColLen := s.nodeOutputMaxLen
	runID := s.runID
	finished := make([]finishedNodeEvent, len(s.finishedNodes))
	copy(finished, s.finishedNodes)
	s.mu.Unlock()

	if !hasCol || runID == "" || len(finished) == 0 {
		return nil
	}

	used := make([]bool, len(finished))
	outputsByExecID := make(map[string]string)

	for _, res := range results {
		foundIdx := -1
		for i, fn := range finished {
			if !used[i] && fn.NodeID == res.ScriptID {
				foundIdx = i
				break
			}
		}
		if foundIdx < 0 {
			for i := range finished {
				if !used[i] {
					foundIdx = i
					break
				}
			}
		}
		if foundIdx >= 0 {
			used[foundIdx] = true
			if finished[foundIdx].ExecutionID != "" {
				outputsByExecID[finished[foundIdx].ExecutionID] = redactNodeOutput(res.ResultsString)
			}
		}
	}

	query := buildUpdateNodeFinishedOutputQuery(s.Driver, TablePipelineEvents)
	var firstErr error
	for execID, out := range outputsByExecID {
		if maxColLen > 0 {
			out = truncateNodeOutput(out, maxColLen)
		}
		if _, err := s.DB.ExecContext(ctx, query, out, runID, execID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RunSummaryRecord represents a pipeline run summary row in TablePipelineRuns.
type RunSummaryRecord struct {
	RunID           string    `json:"run_id"`
	FilePath        string    `json:"file_path"`
	ConfigPath      string    `json:"config_path,omitempty"`
	Status          string    `json:"status"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationMs      int64     `json:"duration_ms"`
	TaskCount       int       `json:"task_count"`
	UserName        string    `json:"user_name,omitempty"`
	OSUserName      string    `json:"os_user_name,omitempty"`
	DBUserName      string    `json:"db_user_name,omitempty"`
	SPID            int64     `json:"spid,omitempty"`
	ApplicationName string    `json:"application_name,omitempty"`
	Hostname        string    `json:"hostname,omitempty"`
	OptionsPath     string    `json:"options_path,omitempty"`
	ErrorClass      string    `json:"error_class,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
}

var defaultRunCols = []string{
	"run_id", "file_path", "config_path", "status", "started_at",
	"finished_at", "duration_ms", "task_count", "user_name", "hostname",
	"options_path", "error_class", "error_message",
}

var knownRunCols = map[string]bool{
	"run_id":             true,
	"file_path":          true,
	"config_path":        true,
	"status":             true,
	"started_at":         true,
	"finished_at":        true,
	"duration_ms":        true,
	"task_count":         true,
	"user_name":          true,
	"os_user_name":       true,
	"db_user_name":       true,
	"database_user_name": true,
	"spid":               true,
	"sspid":              true,
	"application_name":   true,
	"app_name":           true,
	"hostname":           true,
	"options_path":       true,
	"error_class":        true,
	"error_message":      true,
}

func LogRunSummaryToDB(ctx context.Context, db *sql.DB, dialect string, rec RunSummaryRecord) error {
	if db == nil {
		return nil
	}
	if rec.RunID == "" {
		rec.RunID = generateID("run")
	}

	table := resolveTableName(dialect, TablePipelineRuns)
	cols := defaultRunCols
	if rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s WHERE 1=0", table)); err == nil {
		colNames, colErr := rows.Columns()
		rows.Close()
		if colErr == nil && len(colNames) > 0 {
			var filtered []string
			for _, c := range colNames {
				lower := strings.ToLower(strings.TrimSpace(c))
				if knownRunCols[lower] {
					filtered = append(filtered, lower)
				}
			}
			if len(filtered) > 0 {
				cols = filtered
			}
		}
	}

	query := BuildInsertQuery(dialect, TablePipelineRuns, cols)
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		switch c {
		case "run_id":
			args = append(args, rec.RunID)
		case "file_path":
			args = append(args, rec.FilePath)
		case "config_path":
			args = append(args, rec.ConfigPath)
		case "status":
			args = append(args, rec.Status)
		case "started_at":
			args = append(args, rec.StartedAt.UTC())
		case "finished_at":
			args = append(args, rec.FinishedAt.UTC())
		case "duration_ms":
			args = append(args, rec.DurationMs)
		case "task_count":
			args = append(args, rec.TaskCount)
		case "user_name":
			args = append(args, rec.UserName)
		case "os_user_name":
			args = append(args, rec.OSUserName)
		case "db_user_name", "database_user_name":
			args = append(args, rec.DBUserName)
		case "spid", "sspid":
			args = append(args, rec.SPID)
		case "application_name", "app_name":
			args = append(args, rec.ApplicationName)
		case "hostname":
			args = append(args, rec.Hostname)
		case "options_path":
			args = append(args, rec.OptionsPath)
		case "error_class":
			args = append(args, rec.ErrorClass)
		case "error_message":
			args = append(args, rec.ErrorMessage)
		default:
			args = append(args, nil)
		}
	}

	_, err := db.ExecContext(ctx, query, args...)
	return err
}

// PipelineEventRecord represents an event row queried from TablePipelineEvents.
type PipelineEventRecord struct {
	RunID           string    `json:"run_id"`
	ExecutionID     string    `json:"execution_id"`
	SequenceNum     int64     `json:"sequence_num"`
	OccurredAt      time.Time `json:"occurred_at"`
	EventType       string    `json:"event_type"`
	NodeKind        string    `json:"node_kind"`
	NodeID          string    `json:"node_id"`
	Status          string    `json:"status"`
	UserName        string    `json:"user_name,omitempty"`
	OSUserName      string    `json:"os_user_name,omitempty"`
	DBUserName      string    `json:"db_user_name,omitempty"`
	SPID            string    `json:"spid,omitempty"`
	ApplicationName string    `json:"application_name,omitempty"`
	Hostname        string    `json:"hostname,omitempty"`
	OptionsPath     string    `json:"options_path,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	RowsRead        int64     `json:"rows_read"`
	RowsWritten     int64     `json:"rows_written"`
	RowsAffected    int64     `json:"rows_affected"`
	NodeOutput      string    `json:"node_output,omitempty"`
}

func (s *APIServer) QueryPipelineRuns(ctx context.Context, statusFilter string, limit int) ([]RunSummaryRecord, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	table := resolveTableName(s.dialect, TablePipelineRuns)
	whereSQL := ""
	var args []any
	if strings.TrimSpace(statusFilter) != "" {
		whereSQL = fmt.Sprintf(" WHERE status = %s", Placeholder(s.dialect, 1))
		args = append(args, strings.TrimSpace(statusFilter))
	}

	query := fmt.Sprintf("SELECT * FROM %s%s ORDER BY started_at DESC", table, whereSQL)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var runs []RunSummaryRecord
	for rows.Next() && len(runs) < limit {
		rawVals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range rawVals {
			ptrs[i] = &rawVals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		var r RunSummaryRecord
		for i, col := range cols {
			val := rawVals[i]
			if val == nil {
				continue
			}
			switch strings.ToLower(col) {
			case "run_id":
				r.RunID = toString(val)
			case "file_path":
				r.FilePath = toString(val)
			case "config_path":
				r.ConfigPath = toString(val)
			case "status":
				r.Status = toString(val)
			case "started_at":
				r.StartedAt = parseDBTime(val)
			case "finished_at":
				r.FinishedAt = parseDBTime(val)
			case "duration_ms":
				r.DurationMs = toInt64(val)
			case "task_count":
				r.TaskCount = int(toInt64(val))
			case "user_name":
				r.UserName = toString(val)
			case "os_user_name":
				r.OSUserName = toString(val)
			case "db_user_name", "database_user_name":
				r.DBUserName = toString(val)
			case "spid", "sspid":
				r.SPID = toInt64(val)
			case "application_name", "app_name":
				r.ApplicationName = toString(val)
			case "hostname":
				r.Hostname = toString(val)
			case "options_path":
				r.OptionsPath = toString(val)
			case "error_class":
				r.ErrorClass = toString(val)
			case "error_message":
				r.ErrorMessage = toString(val)
			}
		}
		runs = append(runs, r)
	}
	if runs == nil {
		runs = []RunSummaryRecord{}
	}
	return runs, rows.Err()
}

func (s *APIServer) QueryPipelineEvents(ctx context.Context, runID string, limit int) ([]PipelineEventRecord, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	table := resolveTableName(s.dialect, TablePipelineEvents)
	whereSQL := ""
	var args []any
	if strings.TrimSpace(runID) != "" {
		whereSQL = fmt.Sprintf(" WHERE run_id = %s", Placeholder(s.dialect, 1))
		args = append(args, strings.TrimSpace(runID))
	}

	query := fmt.Sprintf("SELECT * FROM %s%s ORDER BY sequence_num ASC", table, whereSQL)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var events []PipelineEventRecord
	for rows.Next() && len(events) < limit {
		rawVals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range rawVals {
			ptrs[i] = &rawVals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		var ev PipelineEventRecord
		for i, col := range cols {
			val := rawVals[i]
			if val == nil {
				continue
			}
			switch strings.ToLower(col) {
			case "run_id":
				ev.RunID = toString(val)
			case "execution_id":
				ev.ExecutionID = toString(val)
			case "sequence_num":
				ev.SequenceNum = toInt64(val)
			case "occurred_at":
				ev.OccurredAt = parseDBTime(val)
			case "event_type":
				ev.EventType = toString(val)
			case "node_kind":
				ev.NodeKind = toString(val)
			case "node_id":
				ev.NodeID = toString(val)
			case "status":
				ev.Status = toString(val)
			case "user_name":
				ev.UserName = toString(val)
			case "os_user_name":
				ev.OSUserName = toString(val)
			case "db_user_name", "database_user_name":
				ev.DBUserName = toString(val)
			case "spid", "sspid":
				ev.SPID = toString(val)
			case "application_name", "app_name":
				ev.ApplicationName = toString(val)
			case "hostname":
				ev.Hostname = toString(val)
			case "options_path":
				ev.OptionsPath = toString(val)
			case "error_message":
				ev.ErrorMessage = toString(val)
			case "rows_read":
				ev.RowsRead = toInt64(val)
			case "rows_written":
				ev.RowsWritten = toInt64(val)
			case "rows_affected":
				ev.RowsAffected = toInt64(val)
			case "node_output":
				ev.NodeOutput = toString(val)
			}
		}
		events = append(events, ev)
	}
	if events == nil {
		events = []PipelineEventRecord{}
	}
	return events, rows.Err()
}

func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case int32:
		return int64(val)
	default:
		return 0
	}
}

func parseDBTime(v any) time.Time {
	switch val := v.(type) {
	case time.Time:
		return val.UTC()
	case []byte:
		return parseTimeString(string(val))
	case string:
		return parseTimeString(val)
	default:
		return time.Time{}
	}
}

func parseTimeString(s string) time.Time {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
