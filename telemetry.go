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
	cols := s.configuredCols
	query := s.insertQuery
	isNumericSPID := s.isNumericSPID
	spid := s.spid
	dbUser := s.dbUser
	appName := s.appName
	s.mu.Unlock()

	if s.DB == nil {
		return nil
	}

	osUser := event.UserName
	if osUser == "" {
		osUser, _ = runtimeIdentity()
	}

	var spidArg any = fmt.Sprintf("%d", spid)
	if isNumericSPID {
		spidArg = spid
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
			args = append(args, spidArg)
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

	query := fmt.Sprintf(
		"SELECT run_id, COALESCE(file_path, ''), COALESCE(config_path, ''), COALESCE(status, ''), started_at, finished_at, COALESCE(duration_ms, 0), COALESCE(task_count, 0), COALESCE(user_name, ''), COALESCE(hostname, ''), COALESCE(options_path, ''), COALESCE(error_class, ''), COALESCE(error_message, '') FROM %s%s ORDER BY started_at DESC",
		table, whereSQL,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []RunSummaryRecord
	for rows.Next() && len(runs) < limit {
		var r RunSummaryRecord
		var startRaw, finRaw any
		if err := rows.Scan(
			&r.RunID,
			&r.FilePath,
			&r.ConfigPath,
			&r.Status,
			&startRaw,
			&finRaw,
			&r.DurationMs,
			&r.TaskCount,
			&r.UserName,
			&r.Hostname,
			&r.OptionsPath,
			&r.ErrorClass,
			&r.ErrorMessage,
		); err != nil {
			return nil, err
		}
		r.StartedAt = parseDBTime(startRaw)
		r.FinishedAt = parseDBTime(finRaw)
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
