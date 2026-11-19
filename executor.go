package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/etl-madness/flow"
	"github.com/etl-madness/flow/flowcrypto"
	"github.com/lestrrat-go/helium"
	"github.com/lestrrat-go/helium/xslt3"
	"github.com/traefik/yaegi/interp"
)

// CliOptionsXML mirrors go-flow's <cli_options> and <flow_cli_options> XML structures (xsd/options.xsd).
type CliOptionsXML struct {
	File            *string             `xml:"file"`
	Format          *string             `xml:"format"`
	XSD             *string             `xml:"xsd"`
	Config          *string             `xml:"config"`
	Validate        *bool               `xml:"validate"`
	Preflight       *bool               `xml:"preflight"`
	Vars            *string             `xml:"vars"`
	Debug           *bool               `xml:"debug"`
	GoPath          *string             `xml:"gopath"`
	XSLT            *string             `xml:"xslt"`
	Out             *string             `xml:"out"`
	NodeOutput      *bool               `xml:"node-output"`
	Encrypted       *bool               `xml:"encrypted"`
	SecureKey       *string             `xml:"secure-key"`
	VerifySignature *bool               `xml:"verify-signature"`
	PublicKey       *string             `xml:"public-key"`
	Cert            *string             `xml:"cert"`
	CACert          *string             `xml:"ca-cert"`
	Keyring         *string             `xml:"keyring"`
	Signature       *string             `xml:"signature"`
	NestedOptions   *CliOptionsXML      `xml:"options"`
	OptionItems     []cliOptionItemElem `xml:"option"`
}

type cliOptionItemElem struct {
	Flag  string `xml:"flag,attr"`
	Value string `xml:",chardata"`
}

// ExecutionRequest supports all CLI, Builder, and db_importer execution options,
// including lookup by artifact type ("pipeline", "options", "config") and database table ID.
type ExecutionRequest struct {
	ArtifactType    string            `json:"artifact_type,omitempty"`
	ID              int64             `json:"id,omitempty"`
	PipelineID      int64             `json:"pipeline_id,omitempty"`
	OptionsID       int64             `json:"options_id,omitempty"`
	ConfigID        int64             `json:"config_id,omitempty"`
	File            string            `json:"file"`
	FileXML         string            `json:"file_xml,omitempty"`
	Config          string            `json:"config,omitempty"`
	ConfigXML       string            `json:"config_xml,omitempty"`
	Options         string            `json:"options,omitempty"`
	OptionsXML      string            `json:"options_xml,omitempty"`
	Format          string            `json:"format,omitempty"`
	XSD             string            `json:"xsd,omitempty"`
	Validate        bool              `json:"validate,omitempty"`
	Preflight       bool              `json:"preflight,omitempty"`
	Vars            map[string]string `json:"vars,omitempty"`
	VarsRaw         string            `json:"vars_raw,omitempty"`
	Debug           bool              `json:"debug,omitempty"`
	GoPath          string            `json:"gopath,omitempty"`
	XSLT            string            `json:"xslt,omitempty"`
	Out             string            `json:"out,omitempty"`
	NodeOutput      bool              `json:"node_output,omitempty"`
	Encrypted       bool              `json:"encrypted,omitempty"`
	SecureKey       string            `json:"secure_key,omitempty"`
	KeyFile         string            `json:"key_file,omitempty"`
	VerifySignature bool              `json:"verify_signature,omitempty"`
	PublicKey       string            `json:"public_key,omitempty"`
	Cert            string            `json:"cert,omitempty"`
	CACert          string            `json:"ca_cert,omitempty"`
	Keyring         string            `json:"keyring,omitempty"`
	Signature       string            `json:"signature,omitempty"`
}

// ExecutionResponse represents the structured result of a pipeline execution or validation.
type ExecutionResponse struct {
	RunID           string              `json:"run_id"`
	Status          string              `json:"status"`
	StartedAt       time.Time           `json:"started_at"`
	FinishedAt      time.Time           `json:"finished_at"`
	DurationMs      int64               `json:"duration_ms"`
	Format          string              `json:"format"`
	FormattedOutput string              `json:"formatted_output,omitempty"`
	Results         []flow.ScriptResult `json:"results,omitempty"`
	ErrorMessage    string              `json:"error_message,omitempty"`
}

// CallbackSink forwards execution events to an arbitrary callback (used for SSE streaming).
type CallbackSink struct {
	OnEvent func(event flow.ExecutionEvent)
}

func (c *CallbackSink) Emit(ctx context.Context, event flow.ExecutionEvent) error {
	if c != nil && c.OnEvent != nil {
		c.OnEvent(event)
	}
	return nil
}

// MultiSink fans out execution events to multiple sinks.
type MultiSink struct {
	Sinks []flow.EventSink
}

func (m *MultiSink) Emit(ctx context.Context, event flow.ExecutionEvent) error {
	var firstErr error
	for _, sink := range m.Sinks {
		if err := sink.Emit(ctx, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// parseExplicitIDRef checks if a string reference uses an explicit ID prefix such as "id:12", "id/12", or "db://id/12".
func parseExplicitIDRef(ref string) (int64, bool) {
	trimmed := strings.TrimSpace(ref)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"db://id/", "db:id:", "id:", "id/"} {
		if strings.HasPrefix(lower, prefix) {
			numStr := strings.TrimSpace(trimmed[len(prefix):])
			if id, err := strconv.ParseInt(numStr, 10, 64); err == nil && id > 0 {
				return id, true
			}
		}
	}
	return 0, false
}

const sqlResourceIdentChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"

type sqlFilterCandidate struct {
	param      string
	defaultCol string
}

var sqlResourceFilterCandidates = []sqlFilterCandidate{
	{"key", "Name"},
	{"name", "Name"},
	{"id", "ID"},
}

// sanitizeSQLResourceIdent validates a single SQL identifier against ^[A-Za-z_][A-Za-z0-9_]{0,127}$
// and reconstructs it strictly from constant alphabet characters so no user-tainted bytes enter SQL text.
func sanitizeSQLResourceIdent(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > 128 {
		return "", fmt.Errorf("invalid SQL identifier %q", raw)
	}
	buf := make([]byte, len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		idx := strings.IndexByte(sqlResourceIdentChars, ch)
		if idx < 0 || (i == 0 && ch >= '0' && ch <= '9') {
			return "", fmt.Errorf("invalid SQL identifier %q", raw)
		}
		buf[i] = sqlResourceIdentChars[idx]
	}
	return string(buf), nil
}

// sanitizeSQLResourceTable validates a SQL table reference ("<table>" or "<schema>.<table>").
func sanitizeSQLResourceTable(raw string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) == 1 {
		tbl, err := sanitizeSQLResourceIdent(parts[0])
		if err != nil {
			return "", "", fmt.Errorf("invalid SQL table %q: %w", raw, err)
		}
		return tbl, tbl, nil
	}
	if len(parts) == 2 {
		schema, err := sanitizeSQLResourceIdent(parts[0])
		if err != nil {
			return "", "", fmt.Errorf("invalid SQL schema in %q: %w", raw, err)
		}
		tbl, err := sanitizeSQLResourceIdent(parts[1])
		if err != nil {
			return "", "", fmt.Errorf("invalid SQL table in %q: %w", raw, err)
		}
		return schema + "." + tbl, tbl, nil
	}
	return "", "", fmt.Errorf("invalid SQL table %q (expected <table> or <schema>.<table>)", raw)
}

func defaultSQLResourceColumn(baseTable string) string {
	switch strings.ToLower(baseTable) {
	case "flow_pipeline_content":
		return "PipelineXML"
	case "flow_options_content":
		return "OptionsXML"
	case "flow_config_content":
		return "ConfigXML"
	default:
		return ""
	}
}

func resolveSQLResourceColumn(params url.Values, baseTable string) (string, error) {
	_, hasColumn := params["column"]
	_, hasCol := params["col"]
	if hasColumn && hasCol {
		return "", fmt.Errorf("specify either column or col, not both")
	}
	rawCol := params.Get("column")
	if hasCol {
		rawCol = params.Get("col")
	}
	if strings.TrimSpace(rawCol) == "" {
		if def := defaultSQLResourceColumn(baseTable); def != "" {
			return def, nil
		}
		return "", fmt.Errorf("missing column parameter for table %q", baseTable)
	}
	return sanitizeSQLResourceIdent(rawCol)
}

func resolveSQLResourceFilter(params url.Values) (string, string, bool, error) {
	defaultKeyCol := ""
	filterVal := ""
	hasFilter := false
	for _, c := range sqlResourceFilterCandidates {
		if _, ok := params[c.param]; ok {
			if hasFilter {
				return "", "", false, fmt.Errorf("specify at most one of key, name, or id")
			}
			hasFilter = true
			defaultKeyCol = c.defaultCol
			filterVal = params.Get(c.param)
		}
	}
	rawKeyCol := strings.TrimSpace(params.Get("key_col"))
	if !hasFilter {
		if rawKeyCol != "" {
			return "", "", false, fmt.Errorf("key_col requires key, name, or id")
		}
		return "", "", false, nil
	}
	if rawKeyCol == "" {
		return defaultKeyCol, filterVal, true, nil
	}
	safeKeyCol, err := sanitizeSQLResourceIdent(rawKeyCol)
	return safeKeyCol, filterVal, err == nil, err
}

func validateSQLResourceParams(params url.Values) error {
	for k, vals := range params {
		switch k {
		case "table", "column", "col", "key_col", "key", "name", "id":
			if len(vals) != 1 {
				return fmt.Errorf("duplicate SQL lookup parameter %q", k)
			}
		default:
			return fmt.Errorf("unsupported SQL lookup parameter %q (expected #table=<schema.table>&column=<xml_col>[&key_col=<col>&key=<val>])", k)
		}
	}
	return nil
}

func sqlResourceFirstPlaceholder(dialect string) string {
	switch strings.ToLower(dialect) {
	case "postgres", "postgresql":
		return "$1"
	case "sqlserver", "mssql", "azuresql":
		return "@p1"
	case "oracle":
		return ":1"
	default:
		return "?"
	}
}

// buildSQLResourceQuery parses a structured URL-query fragment (#table=<schema.table>&column=<xml_col>[&key_col=<col>&key=<val>])
// into a parameterized SELECT statement using strictly validated SQL identifiers and bound filter parameters.
func buildSQLResourceQuery(fragment, dialect string) (string, []any, error) {
	trimmed := strings.TrimSpace(fragment)
	if trimmed == "" {
		return "", nil, fmt.Errorf("empty SQL lookup fragment; expected #table=<schema.table>&column=<xml_col>[&key_col=<col>&key=<val>]")
	}
	params, err := url.ParseQuery(trimmed)
	if err != nil {
		return "", nil, fmt.Errorf("invalid SQL lookup fragment: %w", err)
	}
	if err := validateSQLResourceParams(params); err != nil {
		return "", nil, err
	}
	safeTable, baseTable, err := sanitizeSQLResourceTable(params.Get("table"))
	if err != nil {
		return "", nil, err
	}
	safeCol, err := resolveSQLResourceColumn(params, baseTable)
	if err != nil {
		return "", nil, err
	}
	keyCol, filterVal, hasFilter, err := resolveSQLResourceFilter(params)
	if err != nil {
		return "", nil, err
	}
	if !hasFilter {
		return "SELECT " + safeCol + " FROM " + safeTable, nil, nil
	}
	return "SELECT " + safeCol + " FROM " + safeTable + " WHERE " + keyCol + " = " + sqlResourceFirstPlaceholder(dialect), []any{filterVal}, nil
}

// loadSQLResourceURI resolves a structured sql://<driver>@<dsn>#table=<schema.table>&column=<xml_col>[&key_col=<col>&key=<val>]
// (or db://...) URI using validated identifiers and bound query parameters, returning the artifact bytes.
func loadSQLResourceURI(ctx context.Context, uri string) ([]byte, error) {
	hashIdx := strings.LastIndex(uri, "#")
	if hashIdx < 0 {
		return nil, fmt.Errorf("invalid SQL URI (missing #table=... lookup fragment)")
	}
	dsnPart := strings.TrimSpace(uri[:hashIdx])
	fragment := strings.TrimSpace(uri[hashIdx+1:])
	if dsnPart == "" || fragment == "" {
		return nil, fmt.Errorf("invalid SQL URI (empty DSN or lookup fragment)")
	}

	_, dialect, _ := ParseDSN(dsnPart)
	sqlQuery, args, err := buildSQLResourceQuery(fragment, dialect)
	if err != nil {
		return nil, err
	}

	db, _, err := OpenDatabase(dsnPart)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	qCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var rawVal any
	if err := db.QueryRowContext(qCtx, sqlQuery, args...).Scan(&rawVal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("SQL resource query returned 0 rows")
		}
		return nil, err
	}

	switch v := rawVal.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	case nil:
		return nil, fmt.Errorf("SQL resource query returned NULL")
	default:
		return []byte(fmt.Sprintf("%v", v)), nil
	}
}

// resolveConnectionString expands {{var}} and {{.var}} placeholders in a database connection string
// using variables defined in the pipeline XML, config overrides, VarsRaw, and Vars map.
func resolveConnectionString(dsn string, varConfigs []flow.VariableConfig, varsRaw string, varsMap map[string]string) string {
	resolved := strings.TrimSpace(dsn)
	if !strings.Contains(resolved, "{{") {
		return resolved
	}

	lookup := make(map[string]string, len(varConfigs)+len(varsMap)+8)
	for _, vc := range varConfigs {
		name := strings.TrimSpace(vc.Name)
		if name != "" {
			lookup[name] = vc.Value
		}
	}
	if strings.TrimSpace(varsRaw) != "" {
		for _, pair := range strings.Split(varsRaw, ",") {
			if kv := strings.SplitN(strings.TrimSpace(pair), "=", 2); len(kv) == 2 {
				k := strings.TrimSpace(kv[0])
				if k != "" {
					lookup[k] = strings.TrimSpace(kv[1])
				}
			}
		}
	}
	for k, v := range varsMap {
		trimmedKey := strings.TrimSpace(k)
		if trimmedKey != "" {
			lookup[trimmedKey] = v
		}
	}

	for pass := 0; pass < 3; pass++ {
		changed := false
		for k, v := range lookup {
			p1 := "{{" + k + "}}"
			p2 := "{{." + k + "}}"
			if strings.Contains(resolved, p1) {
				resolved = strings.ReplaceAll(resolved, p1, v)
				changed = true
			}
			if strings.Contains(resolved, p2) {
				resolved = strings.ReplaceAll(resolved, p2, v)
				changed = true
			}
		}
		if !changed || !strings.Contains(resolved, "{{") {
			break
		}
	}
	return resolved
}

// loadInlineOrFileBytes accepts either inline PEM / FLOWSIG / FLOWSIGNED text or a local filesystem path.
func loadInlineOrFileBytes(val string) ([]byte, error) {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return nil, nil
	}
	if strings.Contains(trimmed, "-----BEGIN ") ||
		strings.HasPrefix(trimmed, flowcrypto.SignaturePrefixV1) ||
		strings.HasPrefix(trimmed, flowcrypto.SignedPrefixV1) ||
		strings.Contains(trimmed, "\n") {
		return []byte(trimmed), nil
	}
	if data, err := os.ReadFile(trimmed); err == nil {
		return data, nil
	} else {
		return nil, err
	}
}

// resolveArtifactBytes loads XML content from inline XML, the startup database by table ID or Name
// (TablePipelineContent / TableOptionsContent / TableConfigContent), sql://#query URI, or local file fallback,
// and handles decryption & signature verification.
func (s *APIServer) resolveArtifactBytes(ctx context.Context, artifactType string, artifactID int64, nameOrPath, inlineXML string, req *ExecutionRequest) ([]byte, string, error) {
	key := resolveKey(req.SecureKey, req.KeyFile)
	if key == "" {
		key = s.cfg.DefaultSecureKey
	}

	var rawBytes []byte
	var sourceLabel string

	if strings.TrimSpace(inlineXML) != "" {
		rawBytes = []byte(inlineXML)
		if trimmedName := strings.TrimSpace(nameOrPath); trimmedName != "" {
			sourceLabel = trimmedName
		} else {
			sourceLabel = "inline:" + artifactType
		}
	} else if artifactID > 0 {
		rec, dbErr := s.GetArtifactByID(ctx, artifactType, artifactID, false, "")
		if dbErr != nil {
			return nil, "", fmt.Errorf("failed resolving %s with ID %d from database: %w", artifactType, artifactID, dbErr)
		}
		rawBytes = []byte(rec.Content)
		sourceLabel = fmt.Sprintf("db://%s/id/%d(%s)", artifactType, rec.ID, rec.Name)
	} else if strings.TrimSpace(nameOrPath) != "" {
		ref := strings.TrimSpace(nameOrPath)
		lowerRef := strings.ToLower(ref)
		if (strings.HasPrefix(lowerRef, "sql://") || strings.HasPrefix(lowerRef, "db://")) && strings.Contains(ref, "#") {
			sqlBytes, sqlErr := loadSQLResourceURI(ctx, ref)
			if sqlErr != nil {
				return nil, "", fmt.Errorf("failed resolving %s from SQL URI: %w", artifactType, sqlErr)
			}
			rawBytes = sqlBytes
			sourceLabel = RedactDSN(ref)
		} else if explicitID, ok := parseExplicitIDRef(ref); ok {
			rec, dbErr := s.GetArtifactByID(ctx, artifactType, explicitID, false, "")
			if dbErr != nil {
				return nil, "", fmt.Errorf("failed resolving %s with ID %d from database: %w", artifactType, explicitID, dbErr)
			}
			rawBytes = []byte(rec.Content)
			sourceLabel = fmt.Sprintf("db://%s/id/%d(%s)", artifactType, rec.ID, rec.Name)
		} else {
			lookupName := strings.TrimPrefix(strings.TrimPrefix(ref, "db://"), "db:")
			rec, dbErr := s.GetArtifact(ctx, artifactType, lookupName, false, "")
			if dbErr == nil && rec != nil {
				rawBytes = []byte(rec.Content)
				sourceLabel = "db://" + artifactType + "/" + rec.Name
			} else if numID, numErr := strconv.ParseInt(lookupName, 10, 64); numErr == nil && numID > 0 {
				if recByID, idErr := s.GetArtifactByID(ctx, artifactType, numID, false, ""); idErr == nil && recByID != nil {
					rawBytes = []byte(recByID.Content)
					sourceLabel = fmt.Sprintf("db://%s/id/%d(%s)", artifactType, recByID.ID, recByID.Name)
				} else if data, fileErr := os.ReadFile(ref); fileErr == nil {
					rawBytes = data
					sourceLabel = ref
				} else {
					return nil, "", fmt.Errorf("failed resolving %s %q from database by name (%v), ID %d (%v), or filesystem (%v)", artifactType, ref, dbErr, numID, idErr, fileErr)
				}
			} else if data, fileErr := os.ReadFile(ref); fileErr == nil {
				rawBytes = data
				sourceLabel = ref
			} else {
				return nil, "", fmt.Errorf("failed resolving %s %q from database (%v) or filesystem (%v)", artifactType, ref, dbErr, fileErr)
			}
		}
	} else {
		return nil, "", nil
	}

	rawBytes = []byte(DecodeUploadedBytes(rawBytes))

	var keyOrCertBytes, caCertBytes []byte
	var err error
	if req.PublicKey != "" {
		if keyOrCertBytes, err = loadInlineOrFileBytes(req.PublicKey); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading public key %s: %w", req.PublicKey, err)
		}
	} else if req.Cert != "" {
		if keyOrCertBytes, err = loadInlineOrFileBytes(req.Cert); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading certificate %s: %w", req.Cert, err)
		}
	} else if req.Keyring != "" {
		if keyOrCertBytes, err = loadInlineOrFileBytes(req.Keyring); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading keyring %s: %w", req.Keyring, err)
		}
	}
	if req.CACert != "" {
		if caCertBytes, err = loadInlineOrFileBytes(req.CACert); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading CA certificate %s: %w", req.CACert, err)
		}
	}

	var verResult *flowcrypto.VerificationResult
	isOuterSignedEnvelope := flowcrypto.IsSignedPayload(rawBytes)
	if isOuterSignedEnvelope || req.Signature != "" {
		if len(keyOrCertBytes) == 0 && len(caCertBytes) == 0 {
			return nil, sourceLabel, fmt.Errorf("%s %q has a digital signature, but no public_key or cert was provided", artifactType, sourceLabel)
		}
		if isOuterSignedEnvelope {
			unwrapped, res, err := flowcrypto.VerifyPayloadAutoWithCA(rawBytes, keyOrCertBytes, caCertBytes)
			if err != nil {
				return nil, sourceLabel, fmt.Errorf("digital signature verification failed for %s: %w", sourceLabel, err)
			}
			rawBytes = unwrapped
			verResult = res
		} else {
			sigBytes, err := loadInlineOrFileBytes(req.Signature)
			if err != nil {
				return nil, sourceLabel, fmt.Errorf("failed reading signature %s: %w", req.Signature, err)
			}
			res, err := flowcrypto.VerifyAutoWithCA(rawBytes, sigBytes, keyOrCertBytes, caCertBytes)
			if err != nil {
				return nil, sourceLabel, fmt.Errorf("digital signature verification failed for %s: %w", sourceLabel, err)
			}
			verResult = res
		}
	}

	if req.Encrypted || flowcrypto.IsEncryptedPayload(rawBytes) {
		if key == "" {
			return nil, sourceLabel, fmt.Errorf("%s %q is encrypted, but no secure_key was provided", artifactType, sourceLabel)
		}
		plain, err := flowcrypto.DecryptAuto(rawBytes, key, true)
		if err != nil {
			return nil, sourceLabel, fmt.Errorf("failed decrypting %s %q: %w", artifactType, sourceLabel, err)
		}
		rawBytes = plain

		if flowcrypto.IsSignedPayload(rawBytes) {
			if len(keyOrCertBytes) == 0 && len(caCertBytes) == 0 {
				return nil, sourceLabel, fmt.Errorf("decrypted payload in %s is digitally signed, but no public_key or cert was provided", sourceLabel)
			}
			unwrapped, res, err := flowcrypto.VerifyPayloadAutoWithCA(rawBytes, keyOrCertBytes, caCertBytes)
			if err != nil {
				return nil, sourceLabel, fmt.Errorf("digital signature verification failed for decrypted payload in %s: %w", sourceLabel, err)
			}
			rawBytes = unwrapped
			verResult = res
		}
	}

	if req.VerifySignature && (verResult == nil || !verResult.Valid) {
		return nil, sourceLabel, fmt.Errorf("digital signature verification enforced, but %s %q is not signed", artifactType, sourceLabel)
	}

	return rawBytes, sourceLabel, nil
}

func (s *APIServer) applyOptionsXMLToRequest(ctx context.Context, req *ExecutionRequest) (string, error) {
	if req.OptionsID <= 0 && strings.TrimSpace(req.Options) == "" && strings.TrimSpace(req.OptionsXML) == "" {
		return "", nil
	}
	optBytes, optLabel, err := s.resolveArtifactBytes(ctx, "options", req.OptionsID, req.Options, req.OptionsXML, req)
	if err != nil {
		return "", err
	}
	if len(optBytes) == 0 {
		return "", nil
	}

	var opts CliOptionsXML
	if err := xml.Unmarshal(optBytes, &opts); err != nil {
		return optLabel, fmt.Errorf("invalid options XML in %s: %w", optLabel, err)
	}

	active := &opts
	if opts.NestedOptions != nil {
		active = opts.NestedOptions
	}

	applyFlagValue := func(flagName, rawVal string) {
		val := strings.TrimSpace(rawVal)
		switch strings.ToLower(strings.TrimLeft(strings.TrimSpace(flagName), "-")) {
		case "file", "f":
			if req.File == "" && req.FileXML == "" && req.PipelineID <= 0 && val != "" {
				req.File = val
			}
		case "format":
			if req.Format == "" && val != "" {
				req.Format = val
			}
		case "xsd":
			if req.XSD == "" && val != "" {
				req.XSD = val
			}
		case "config", "c":
			if req.Config == "" && req.ConfigXML == "" && req.ConfigID <= 0 && val != "" {
				req.Config = val
			}
		case "validate":
			if !req.Validate {
				if b, err := strconv.ParseBool(val); err == nil {
					req.Validate = b
				}
			}
		case "preflight":
			if !req.Preflight {
				if b, err := strconv.ParseBool(val); err == nil {
					req.Preflight = b
				}
			}
		case "vars":
			if req.VarsRaw == "" && val != "" {
				req.VarsRaw = val
			}
		case "debug":
			if !req.Debug {
				if b, err := strconv.ParseBool(val); err == nil {
					req.Debug = b
				}
			}
		case "gopath":
			if req.GoPath == "" && val != "" {
				req.GoPath = val
			}
		case "xslt":
			if req.XSLT == "" && val != "" {
				req.XSLT = val
			}
		case "out":
			if req.Out == "" && val != "" {
				req.Out = val
			}
		case "node-output", "node_output":
			if !req.NodeOutput {
				if b, err := strconv.ParseBool(val); err == nil {
					req.NodeOutput = b
				}
			}
		case "encrypted":
			if !req.Encrypted {
				if b, err := strconv.ParseBool(val); err == nil {
					req.Encrypted = b
				}
			}
		case "secure-key", "secure_key":
			if req.SecureKey == "" && val != "" {
				req.SecureKey = val
			}
		case "verify-signature", "verify_signature":
			if !req.VerifySignature {
				if b, err := strconv.ParseBool(val); err == nil {
					req.VerifySignature = b
				}
			}
		case "public-key", "public_key":
			if req.PublicKey == "" && val != "" {
				req.PublicKey = val
			}
		case "cert":
			if req.Cert == "" && val != "" {
				req.Cert = val
			}
		case "ca-cert", "ca_cert":
			if req.CACert == "" && val != "" {
				req.CACert = val
			}
		case "keyring":
			if req.Keyring == "" && val != "" {
				req.Keyring = val
			}
		case "signature":
			if req.Signature == "" && val != "" {
				req.Signature = val
			}
		}
	}

	// Apply XML defaults where request fields were not explicitly set
	if req.File == "" && req.FileXML == "" && req.PipelineID <= 0 && active.File != nil {
		req.File = strings.TrimSpace(*active.File)
	}
	if req.Format == "" && active.Format != nil {
		req.Format = strings.TrimSpace(*active.Format)
	}
	if req.XSD == "" && active.XSD != nil {
		req.XSD = strings.TrimSpace(*active.XSD)
	}
	if req.Config == "" && req.ConfigXML == "" && req.ConfigID <= 0 && active.Config != nil {
		req.Config = strings.TrimSpace(*active.Config)
	}
	if !req.Validate && active.Validate != nil {
		req.Validate = *active.Validate
	}
	if !req.Preflight && active.Preflight != nil {
		req.Preflight = *active.Preflight
	}
	if req.VarsRaw == "" && active.Vars != nil {
		req.VarsRaw = strings.TrimSpace(*active.Vars)
	}
	if !req.Debug && active.Debug != nil {
		req.Debug = *active.Debug
	}
	if req.GoPath == "" && active.GoPath != nil {
		req.GoPath = strings.TrimSpace(*active.GoPath)
	}
	if req.XSLT == "" && active.XSLT != nil {
		req.XSLT = strings.TrimSpace(*active.XSLT)
	}
	if req.Out == "" && active.Out != nil {
		req.Out = strings.TrimSpace(*active.Out)
	}
	if !req.NodeOutput && active.NodeOutput != nil {
		req.NodeOutput = *active.NodeOutput
	}
	if !req.Encrypted && active.Encrypted != nil {
		req.Encrypted = *active.Encrypted
	}
	if req.SecureKey == "" && active.SecureKey != nil {
		req.SecureKey = strings.TrimSpace(*active.SecureKey)
	}
	if !req.VerifySignature && active.VerifySignature != nil {
		req.VerifySignature = *active.VerifySignature
	}
	if req.PublicKey == "" && active.PublicKey != nil {
		req.PublicKey = strings.TrimSpace(*active.PublicKey)
	}
	if req.Cert == "" && active.Cert != nil {
		req.Cert = strings.TrimSpace(*active.Cert)
	}
	if req.CACert == "" && active.CACert != nil {
		req.CACert = strings.TrimSpace(*active.CACert)
	}
	if req.Keyring == "" && active.Keyring != nil {
		req.Keyring = strings.TrimSpace(*active.Keyring)
	}
	if req.Signature == "" && active.Signature != nil {
		req.Signature = strings.TrimSpace(*active.Signature)
	}

	for _, item := range opts.OptionItems {
		applyFlagValue(item.Flag, item.Value)
	}
	if opts.NestedOptions != nil {
		for _, item := range opts.NestedOptions.OptionItems {
			applyFlagValue(item.Flag, item.Value)
		}
	}

	return optLabel, nil
}

// resolveRegisteredArtifactRecord attempts to resolve an artifact strictly from the database
// (by explicit ID, explicit "id:N" prefix, Name, or numeric Name fallback) without touching external SQL URIs or local files.
func (s *APIServer) resolveRegisteredArtifactRecord(ctx context.Context, artifactType string, artifactID int64, nameOrPath string) (*ArtifactRecord, bool, error) {
	if artifactID > 0 {
		rec, err := s.GetArtifactByID(ctx, artifactType, artifactID, false, "")
		if err != nil {
			return nil, false, err
		}
		return rec, true, nil
	}
	ref := strings.TrimSpace(nameOrPath)
	if ref == "" {
		return nil, false, nil
	}
	lowerRef := strings.ToLower(ref)
	if (strings.HasPrefix(lowerRef, "sql://") || strings.HasPrefix(lowerRef, "db://")) && strings.Contains(ref, "#") {
		return nil, false, nil
	}
	if explicitID, ok := parseExplicitIDRef(ref); ok {
		rec, err := s.GetArtifactByID(ctx, artifactType, explicitID, false, "")
		if err != nil {
			return nil, false, err
		}
		return rec, true, nil
	}
	lookupName := strings.TrimPrefix(strings.TrimPrefix(ref, "db://"), "db:")
	if rec, err := s.GetArtifact(ctx, artifactType, lookupName, false, ""); err == nil && rec != nil {
		return rec, true, nil
	}
	if numID, numErr := strconv.ParseInt(lookupName, 10, 64); numErr == nil && numID > 0 {
		if recByID, idErr := s.GetArtifactByID(ctx, artifactType, numID, false, ""); idErr == nil && recByID != nil {
			return recByID, true, nil
		}
	}
	return nil, false, nil
}

// AuthorizeExecutionRequest verifies that user holds granular 'execute' permission on the resolved target pipeline
// (including after <cli_options> expansion) and blocks inline XML, SQL URI, or filesystem escapes for scoped users.
func (s *APIServer) AuthorizeExecutionRequest(ctx context.Context, user *APIUser, req ExecutionRequest) (string, error) {
	if user == nil {
		return "", nil
	}
	if user.HasPermission(PermAll) {
		return "", nil
	}

	canInlineOrExternal := s.CanUserExecuteInlineOrExternal(ctx, user)

	// Normalize ArtifactType + ID on a working copy
	resolvedReq := req
	canonicalType := "pipeline"
	if strings.TrimSpace(resolvedReq.ArtifactType) != "" {
		_, _, ct, err := ResolveArtifactTarget(resolvedReq.ArtifactType)
		if err != nil {
			return "", err
		}
		canonicalType = ct
		resolvedReq.ArtifactType = ct
	}
	if resolvedReq.ID > 0 {
		switch canonicalType {
		case "pipeline":
			if resolvedReq.PipelineID <= 0 {
				resolvedReq.PipelineID = resolvedReq.ID
			}
		case "options":
			if resolvedReq.OptionsID <= 0 {
				resolvedReq.OptionsID = resolvedReq.ID
			}
		case "config":
			if resolvedReq.ConfigID <= 0 {
				resolvedReq.ConfigID = resolvedReq.ID
			}
		}
	}

	checkDisallowedInlineOrFS := func(r *ExecutionRequest) error {
		if canInlineOrExternal {
			return nil
		}
		if strings.TrimSpace(r.FileXML) != "" || strings.TrimSpace(r.ConfigXML) != "" || strings.TrimSpace(r.OptionsXML) != "" {
			return fmt.Errorf("%w: inline XML execution is not permitted for scoped users", ErrPipelineForbidden)
		}
		if strings.TrimSpace(r.XSD) != "" || strings.TrimSpace(r.Out) != "" || strings.TrimSpace(r.KeyFile) != "" {
			return fmt.Errorf("%w: local filesystem XSD/output/key_file options are not permitted for scoped users", ErrPipelineForbidden)
		}
		return nil
	}

	if err := checkDisallowedInlineOrFS(&resolvedReq); err != nil {
		return "", err
	}

	// Ensure any referenced options profile comes from the DB for scoped users before expanding it
	if resolvedReq.OptionsID > 0 || strings.TrimSpace(resolvedReq.Options) != "" || strings.TrimSpace(resolvedReq.OptionsXML) != "" {
		if !canInlineOrExternal {
			if _, ok, _ := s.resolveRegisteredArtifactRecord(ctx, "options", resolvedReq.OptionsID, resolvedReq.Options); !ok {
				return "", fmt.Errorf("%w: options reference must be a registered database options artifact", ErrPipelineForbidden)
			}
		}
		if _, optErr := s.applyOptionsXMLToRequest(ctx, &resolvedReq); optErr != nil && !canInlineOrExternal {
			return "", optErr
		}
		if err := checkDisallowedInlineOrFS(&resolvedReq); err != nil {
			return "", err
		}
	}

	// Direct config-only execution without a pipeline
	if (resolvedReq.ArtifactType == "config" || resolvedReq.ConfigID > 0) &&
		resolvedReq.PipelineID <= 0 &&
		strings.TrimSpace(resolvedReq.File) == "" &&
		strings.TrimSpace(resolvedReq.FileXML) == "" {
		if !canInlineOrExternal {
			return "", fmt.Errorf("%w: direct config execution without a target pipeline is not permitted for scoped users", ErrPipelineForbidden)
		}
		return "", nil
	}

	if strings.TrimSpace(resolvedReq.FileXML) != "" {
		if !canInlineOrExternal {
			return "", fmt.Errorf("%w: inline XML execution is not permitted for scoped users", ErrPipelineForbidden)
		}
		return "inline", nil
	}

	targetFile := strings.TrimSpace(resolvedReq.File)
	if resolvedReq.PipelineID <= 0 && targetFile == "" {
		targetFile = "scripts.xml"
	}

	rec, foundInDB, dbErr := s.resolveRegisteredArtifactRecord(ctx, "pipeline", resolvedReq.PipelineID, targetFile)
	if foundInDB && rec != nil {
		allowed, reason, aclErr := s.EvaluatePipelineAccess(ctx, user, rec.Name, nil, ActionExecute)
		if aclErr != nil {
			return rec.Name, aclErr
		}
		if !allowed {
			return rec.Name, fmt.Errorf("%w: %s", ErrPipelineForbidden, reason)
		}
		// Verify auxiliary config / XSLT references do not escape to local filesystem for scoped users
		if !canInlineOrExternal {
			if resolvedReq.ConfigID > 0 || strings.TrimSpace(resolvedReq.Config) != "" {
				if _, ok, _ := s.resolveRegisteredArtifactRecord(ctx, "config", resolvedReq.ConfigID, resolvedReq.Config); !ok {
					return rec.Name, fmt.Errorf("%w: config %q is not a registered database config artifact", ErrPipelineForbidden, resolvedReq.Config)
				}
			}
			if strings.TrimSpace(resolvedReq.XSLT) != "" {
				xsltRec, ok, _ := s.resolveRegisteredArtifactRecord(ctx, "pipeline", 0, resolvedReq.XSLT)
				if !ok || xsltRec == nil {
					return rec.Name, fmt.Errorf("%w: XSLT %q is not a registered database artifact", ErrPipelineForbidden, resolvedReq.XSLT)
				}
				if okXSLT, xsltReason, _ := s.EvaluatePipelineAccess(ctx, user, xsltRec.Name, nil, ActionRead); !okXSLT {
					return rec.Name, fmt.Errorf("%w: %s", ErrPipelineForbidden, xsltReason)
				}
			}
		}
		return rec.Name, nil
	}

	if !canInlineOrExternal {
		if dbErr != nil && !errors.Is(dbErr, ErrArtifactNotFound) {
			return targetFile, dbErr
		}
		return targetFile, fmt.Errorf("%w: pipeline %q is not accessible or not registered in database", ErrPipelineForbidden, targetFile)
	}

	return targetFile, nil
}

// ExecutePipeline executes or validates a pipeline with all available CLI/Builder options,
// supporting resolution by artifact name, inline XML, local path, or database table ID.
func (s *APIServer) ExecutePipeline(ctx context.Context, req ExecutionRequest, onEvent func(flow.ExecutionEvent)) (*ExecutionResponse, error) {
	start := time.Now().UTC()

	if user := UserFromContext(ctx); user != nil {
		if _, authErr := s.AuthorizeExecutionRequest(ctx, user, req); authErr != nil {
			return nil, authErr
		}
	}

	// Normalize ArtifactType + ID into the corresponding typed ID field (PipelineID, OptionsID, or ConfigID)
	canonicalType := "pipeline"
	if strings.TrimSpace(req.ArtifactType) != "" {
		_, _, ct, err := ResolveArtifactTarget(req.ArtifactType)
		if err != nil {
			return nil, err
		}
		canonicalType = ct
		req.ArtifactType = ct
	}
	if req.ID > 0 {
		switch canonicalType {
		case "pipeline":
			if req.PipelineID <= 0 {
				req.PipelineID = req.ID
			}
		case "options":
			if req.OptionsID <= 0 {
				req.OptionsID = req.ID
			}
		case "config":
			if req.ConfigID <= 0 {
				req.ConfigID = req.ID
			}
		}
	}

	optionsLabel, err := s.applyOptionsXMLToRequest(ctx, &req)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.Format) == "" {
		req.Format = "json"
	}

	var pipelineBytes []byte
	var fileLabel string
	var configUsedAsPrimary bool

	// If executing a config artifact directly (e.g. /api/v1/execute/config/{id}) without a separate pipeline file/ID,
	// load the config XML from TableConfigContent as the primary XML document.
	if (req.ArtifactType == "config" || req.ConfigID > 0) && req.PipelineID <= 0 && strings.TrimSpace(req.File) == "" && strings.TrimSpace(req.FileXML) == "" {
		pipelineBytes, fileLabel, err = s.resolveArtifactBytes(ctx, "config", req.ConfigID, req.Config, req.ConfigXML, &req)
		if err != nil {
			return nil, err
		}
		configUsedAsPrimary = true
	} else {
		if req.PipelineID <= 0 && strings.TrimSpace(req.File) == "" && strings.TrimSpace(req.FileXML) == "" {
			req.File = "scripts.xml"
		}
		pipelineBytes, fileLabel, err = s.resolveArtifactBytes(ctx, "pipeline", req.PipelineID, req.File, req.FileXML, &req)
		if err != nil {
			return nil, err
		}
	}

	// Optional XSLT transformation
	if strings.TrimSpace(req.XSLT) != "" {
		xsltBytes, _, xsltErr := s.resolveArtifactBytes(ctx, "pipeline", 0, req.XSLT, "", &req)
		if xsltErr != nil {
			return nil, fmt.Errorf("failed loading XSLT stylesheet %q: %w", req.XSLT, xsltErr)
		}
		transformed, err := transformXMLWithXSLT(ctx, pipelineBytes, xsltBytes, req.Out)
		if err != nil {
			return nil, fmt.Errorf("XSLT transformation failed: %w", err)
		}
		pipelineBytes = transformed
	}

	// Optional XSD schema validation
	if strings.TrimSpace(req.XSD) != "" {
		tmpXML, err := writeTempFile("validate-*.xml", pipelineBytes)
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmpXML)
		if err := flow.ValidateXSD(tmpXML, req.XSD); err != nil {
			return nil, fmt.Errorf("XSD validation failed: %w", err)
		}
	}

	parsedCfg, err := flow.ParseXMLConfig(pipelineBytes)
	if err != nil {
		return nil, fmt.Errorf("XML parsing error in %s: %w", fileLabel, err)
	}

	varConfigs := parsedCfg.Variables
	dbConfigs := parsedCfg.Databases
	preflightNodes := parsedCfg.PreflightNodes
	flowNodes := parsedCfg.FlowNodes

	var configLabel string
	if !configUsedAsPrimary && (req.ConfigID > 0 || strings.TrimSpace(req.Config) != "" || strings.TrimSpace(req.ConfigXML) != "") {
		cfgBytes, lbl, err := s.resolveArtifactBytes(ctx, "config", req.ConfigID, req.Config, req.ConfigXML, &req)
		if err != nil {
			return nil, err
		}
		configLabel = lbl
		overrideCfg, err := flow.ParseXMLConfig(cfgBytes)
		if err != nil {
			return nil, fmt.Errorf("XML parsing error in config %s: %w", configLabel, err)
		}
		varConfigs = append(varConfigs, overrideCfg.Variables...)
		dbConfigs = append(dbConfigs, overrideCfg.Databases...)
		preflightNodes = append(preflightNodes, overrideCfg.PreflightNodes...)
		flowNodes = append(flowNodes, overrideCfg.FlowNodes...)
	} else if configUsedAsPrimary {
		configLabel = fileLabel
	}

	if err := flow.ValidateAST(preflightNodes, flowNodes, dbConfigs); err != nil {
		return nil, fmt.Errorf("AST validation error: %w", err)
	}

	if req.Validate {
		fin := time.Now().UTC()
		res := []flow.ScriptResult{{
			ScriptID:      "system",
			ReturnCode:    0,
			ResultsString: "XML pipeline schema (XSD) and AST structure are valid.",
		}}
		return &ExecutionResponse{
			RunID:           generateID("validate"),
			Status:          string(flow.RunStatusSucceeded),
			StartedAt:       start,
			FinishedAt:      fin,
			DurationMs:      fin.Sub(start).Milliseconds(),
			Format:          req.Format,
			FormattedOutput: formatScriptResults(req.Format, res),
			Results:         res,
		}, nil
	}

	nodes := flowNodes
	if req.Preflight {
		nodes = preflightNodes
	}

	registry := flow.NewRegistry()
	if err := registry.InitVariables(varConfigs); err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.VarsRaw) != "" {
		for _, pair := range strings.Split(req.VarsRaw, ",") {
			if kv := strings.SplitN(strings.TrimSpace(pair), "=", 2); len(kv) == 2 {
				registry.SetVar(strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1]))
			}
		}
	}
	for k, v := range req.Vars {
		registry.SetVar(strings.TrimSpace(k), v)
	}

	if err := registry.InitDatabases(dbConfigs); err != nil {
		return nil, err
	}
	defer registry.CloseDatabases()

	executor := flow.NewExecutor(registry)
	if optionsLabel != "" {
		executor.SetOptionsPath(optionsLabel)
	}
	executor.SetVerbose(req.Debug || s.cfg.Debug)
	if strings.TrimSpace(req.GoPath) != "" {
		executor.SetGoPath(strings.TrimSpace(req.GoPath))
	} else if envGoPath := os.Getenv("GOPATH"); envGoPath != "" {
		executor.SetGoPath(envGoPath)
	}
	executor.SetInterpHook(func(opts *interp.Options) {
		opts.Unrestricted = false
	})

	// Configure database logging sink (uses pipeline's log_db / logg_db if defined and reachable, otherwise defaults to API startup DB)
	logDB := s.db
	logDialect := s.dialect
	var closeLogDB bool

	for i := len(dbConfigs) - 1; i >= 0; i-- {
		dbName := strings.TrimSpace(dbConfigs[i].Name)
		if strings.EqualFold(dbName, "log_db") || strings.EqualFold(dbName, "logg_db") {
			expandedDSN := resolveConnectionString(dbConfigs[i].ConnectionString, varConfigs, req.VarsRaw, req.Vars)
			if strings.TrimSpace(expandedDSN) != "" && !strings.Contains(expandedDSN, "{{") {
				customDB, customDialect, openErr := OpenDatabaseWithDriver(expandedDSN, dbConfigs[i].Driver)
				if openErr != nil {
					log.Printf("WARNING: failed opening pipeline %s (%s): %v — falling back to API startup database", dbName, RedactDSN(expandedDSN), openErr)
				} else {
					pingCtx, pingCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					pingErr := customDB.PingContext(pingCtx)
					pingCancel()
					if pingErr != nil {
						log.Printf("WARNING: failed connecting to pipeline %s (%s): %v — falling back to API startup database", dbName, RedactDSN(expandedDSN), pingErr)
						_ = customDB.Close()
					} else {
						logDB = customDB
						logDialect = customDialect
						closeLogDB = true
					}
				}
			} else if strings.Contains(expandedDSN, "{{") {
				log.Printf("WARNING: pipeline %s connection string %q has unresolved variable placeholder — falling back to API startup database", dbName, RedactDSN(expandedDSN))
			}
			break
		}
	}
	if closeLogDB {
		defer logDB.Close()
	}

	dbSink := NewDatabaseSink(logDB, logDialect, req.NodeOutput, req.Debug || s.cfg.Debug)
	dbSink.SetRegistry(registry)
	dbSink.RegisterNodes(flowNodes)
	if len(preflightNodes) > 0 {
		dbSink.RegisterNodes(preflightNodes)
	}
	var sinks []flow.EventSink
	sinks = append(sinks, dbSink)
	if onEvent != nil {
		sinks = append(sinks, &CallbackSink{OnEvent: onEvent})
	}
	executor.SetEventSink(&MultiSink{Sinks: sinks})

	scriptResults, execErr := executor.Execute(ctx, nodes)
	finishedAt := time.Now().UTC()

	logCtx, logCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer logCancel()

	if req.NodeOutput {
		if err := dbSink.RecordNodeOutputs(logCtx, scriptResults); err != nil {
			log.Printf("WARNING: failed recording node outputs for run: %v", err)
		}
	}

	runID, statusStr, errClassStr, errMsgStr := dbSink.RunDetails()
	if runID == "" {
		runID = generateID("run")
	}
	if statusStr == "" {
		if execErr != nil {
			statusStr = string(flow.RunStatusFailed)
			if errMsgStr == "" {
				errMsgStr = execErr.Error()
			}
		} else {
			statusStr = string(flow.RunStatusSucceeded)
		}
	}

	osUser, hostname := runtimeIdentity()
	if authUser := UserFromContext(ctx); authUser != nil && authUser.Username != "" {
		osUser = authUser.Username
	}

	dbSPID, dbUser, appName := dbSink.SessionIdentity()
	summary := RunSummaryRecord{
		RunID:           runID,
		FilePath:        fileLabel,
		ConfigPath:      configLabel,
		Status:          statusStr,
		StartedAt:       start,
		FinishedAt:      finishedAt,
		DurationMs:      finishedAt.Sub(start).Milliseconds(),
		TaskCount:       len(nodes),
		UserName:        osUser,
		OSUserName:      osUser,
		DBUserName:      dbUser,
		SPID:            dbSPID,
		ApplicationName: appName,
		Hostname:        hostname,
		OptionsPath:     optionsLabel,
		ErrorClass:      errClassStr,
		ErrorMessage:    errMsgStr,
	}
	if err := LogRunSummaryToDB(logCtx, logDB, logDialect, summary); err != nil {
		log.Printf("WARNING: failed logging run summary (%s) to pipeline_runs: %v", runID, err)
	}

	resp := &ExecutionResponse{
		RunID:           runID,
		Status:          statusStr,
		StartedAt:       start,
		FinishedAt:      finishedAt,
		DurationMs:      finishedAt.Sub(start).Milliseconds(),
		Format:          req.Format,
		FormattedOutput: formatScriptResults(req.Format, scriptResults),
		Results:         scriptResults,
		ErrorMessage:    errMsgStr,
	}
	return resp, execErr
}

func transformXMLWithXSLT(ctx context.Context, xmlInput, xsltInput []byte, outPath string) ([]byte, error) {
	parser := helium.NewParser()
	stylesheetDoc, err := parser.Parse(ctx, xsltInput)
	if err != nil {
		return nil, fmt.Errorf("failed to parse XSLT input: %w", err)
	}
	stylesheet, err := xslt3.NewCompiler().Compile(ctx, stylesheetDoc)
	if err != nil {
		return nil, fmt.Errorf("failed to compile XSLT stylesheet: %w", err)
	}
	sourceDoc, err := parser.Parse(ctx, xmlInput)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source XML input: %w", err)
	}
	var buf bytes.Buffer
	if err := stylesheet.Transform(sourceDoc).WriteTo(ctx, &buf); err != nil {
		return nil, fmt.Errorf("failed to execute XSLT transformation: %w", err)
	}
	outBytes := buf.Bytes()
	if strings.TrimSpace(outPath) != "" {
		if err := os.WriteFile(strings.TrimSpace(outPath), outBytes, 0644); err != nil {
			return nil, fmt.Errorf("failed writing XSLT output to %s: %w", outPath, err)
		}
	}
	return outBytes, nil
}

func writeTempFile(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	f.Close()
	return name, nil
}

func formatScriptResults(format string, results []flow.ScriptResult) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		b, _ := json.Marshal(results)
		return string(b)
	case "jsonpretty", "prettyjson":
		b, _ := json.MarshalIndent(results, "", "  ")
		return string(b)
	case "markdown", "md", "table":
		var buf bytes.Buffer
		buf.WriteString("| Script ID | Return Code | Duration | Output |\n|---|---|---|---|\n")
		for _, r := range results {
			out := strings.ReplaceAll(r.ResultsString, "\n", " ")
			fmt.Fprintf(&buf, "| %s | %d | %v | %s |\n", r.ScriptID, r.ReturnCode, r.Duration, out)
		}
		return buf.String()
	case "text":
		var buf bytes.Buffer
		for _, r := range results {
			fmt.Fprintf(&buf, "[%s] (rc=%d, duration=%v): %s\n", r.ScriptID, r.ReturnCode, r.Duration, r.ResultsString)
		}
		return buf.String()
	case "csv":
		fallthrough
	default:
		var buf bytes.Buffer
		for _, r := range results {
			fmt.Fprintf(&buf, "%s,%d,%v,%s\n", r.ScriptID, r.ReturnCode, r.Duration, r.ResultsString)
		}
		return buf.String()
	}
}
