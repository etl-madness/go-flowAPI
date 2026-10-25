package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/etl-madness/flow"
	"github.com/etl-madness/go-flow/pkg/flowcrypto"
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

// loadSQLResourceURI executes a sql://<driver>@<dsn>#<SQL_QUERY> URI and returns the first column of the first row.
func loadSQLResourceURI(ctx context.Context, uri string) ([]byte, error) {
	hashIdx := strings.LastIndex(uri, "#")
	if hashIdx < 0 {
		return nil, fmt.Errorf("invalid SQL URI (missing #<SQL_QUERY> fragment)")
	}
	dsnPart := strings.TrimSpace(uri[:hashIdx])
	query := strings.TrimSpace(uri[hashIdx+1:])
	if dsnPart == "" || query == "" {
		return nil, fmt.Errorf("invalid SQL URI (empty DSN or query)")
	}

	db, _, err := OpenDatabase(dsnPart)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	qCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	rows, err := db.QueryContext(qCtx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("SQL resource query returned 0 rows")
	}

	cols, err := rows.Columns()
	if err != nil || len(cols) == 0 {
		return nil, fmt.Errorf("SQL resource query returned no columns")
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}

	switch v := vals[0].(type) {
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

// ExecutePipeline executes or validates a pipeline with all available CLI/Builder options,
// supporting resolution by artifact name, inline XML, local path, or database table ID.
func (s *APIServer) ExecutePipeline(ctx context.Context, req ExecutionRequest, onEvent func(flow.ExecutionEvent)) (*ExecutionResponse, error) {
	start := time.Now().UTC()

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
