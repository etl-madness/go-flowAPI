package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/etl-madness/flow"
	"github.com/etl-madness/go-flow/pkg/flowcrypto"
	"github.com/lestrrat-go/helium"
	"github.com/lestrrat-go/helium/xslt3"
	"github.com/traefik/yaegi/interp"
)

// CliOptionsXML mirrors go-flow's <cli_options> XML structure (xsd/options.xsd).
type CliOptionsXML struct {
	XMLName         xml.Name `xml:"cli_options"`
	File            *string  `xml:"file"`
	Format          *string  `xml:"format"`
	XSD             *string  `xml:"xsd"`
	Config          *string  `xml:"config"`
	Validate        *bool    `xml:"validate"`
	Preflight       *bool    `xml:"preflight"`
	Vars            *string  `xml:"vars"`
	Debug           *bool    `xml:"debug"`
	GoPath          *string  `xml:"gopath"`
	XSLT            *string  `xml:"xslt"`
	Out             *string  `xml:"out"`
	NodeOutput      *bool    `xml:"node-output"`
	Encrypted       *bool    `xml:"encrypted"`
	SecureKey       *string  `xml:"secure-key"`
	VerifySignature *bool    `xml:"verify-signature"`
	PublicKey       *string  `xml:"public-key"`
	Cert            *string  `xml:"cert"`
	CACert          *string  `xml:"ca-cert"`
	Keyring         *string  `xml:"keyring"`
	Signature       *string  `xml:"signature"`
}

// ExecutionRequest supports all CLI, Builder, and db_importer execution options.
type ExecutionRequest struct {
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

// resolveArtifactBytes loads XML content from inline XML, the startup database (TablePipelineContent /
// TableOptionsContent / TableConfigContent), or local file fallback, and handles decryption & signature verification.
func (s *APIServer) resolveArtifactBytes(ctx context.Context, artifactType, nameOrPath, inlineXML string, req *ExecutionRequest) ([]byte, string, error) {
	key := resolveKey(req.SecureKey, req.KeyFile)
	if key == "" {
		key = s.cfg.DefaultSecureKey
	}

	var rawBytes []byte
	var sourceLabel string

	if strings.TrimSpace(inlineXML) != "" {
		rawBytes = []byte(inlineXML)
		sourceLabel = "inline:" + artifactType
	} else if strings.TrimSpace(nameOrPath) != "" {
		ref := strings.TrimSpace(nameOrPath)
		lookupName := strings.TrimPrefix(strings.TrimPrefix(ref, "db://"), "db:")
		rec, dbErr := s.GetArtifact(ctx, artifactType, lookupName, false, "")
		if dbErr == nil && rec != nil {
			rawBytes = []byte(rec.Content)
			sourceLabel = "db://" + artifactType + "/" + rec.Name
		} else if data, fileErr := os.ReadFile(ref); fileErr == nil {
			rawBytes = data
			sourceLabel = ref
		} else {
			return nil, "", fmt.Errorf("failed resolving %s %q from database (%v) or filesystem (%v)", artifactType, ref, dbErr, fileErr)
		}
	} else {
		return nil, "", nil
	}

	rawBytes = []byte(DecodeUploadedBytes(rawBytes))

	var keyOrCertBytes, caCertBytes []byte
	var err error
	if req.PublicKey != "" {
		if keyOrCertBytes, err = os.ReadFile(req.PublicKey); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading public key %s: %w", req.PublicKey, err)
		}
	} else if req.Cert != "" {
		if keyOrCertBytes, err = os.ReadFile(req.Cert); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading certificate %s: %w", req.Cert, err)
		}
	} else if req.Keyring != "" {
		if keyOrCertBytes, err = os.ReadFile(req.Keyring); err != nil {
			return nil, sourceLabel, fmt.Errorf("failed reading keyring %s: %w", req.Keyring, err)
		}
	}
	if req.CACert != "" {
		if caCertBytes, err = os.ReadFile(req.CACert); err != nil {
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
			sigBytes, err := os.ReadFile(req.Signature)
			if err != nil {
				return nil, sourceLabel, fmt.Errorf("failed reading signature file %s: %w", req.Signature, err)
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
	if strings.TrimSpace(req.Options) == "" && strings.TrimSpace(req.OptionsXML) == "" {
		return "", nil
	}
	optBytes, optLabel, err := s.resolveArtifactBytes(ctx, "options", req.Options, req.OptionsXML, req)
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

	// Apply XML defaults where request fields were not explicitly set
	if req.File == "" && req.FileXML == "" && opts.File != nil {
		req.File = strings.TrimSpace(*opts.File)
	}
	if req.Format == "" && opts.Format != nil {
		req.Format = strings.TrimSpace(*opts.Format)
	}
	if req.XSD == "" && opts.XSD != nil {
		req.XSD = strings.TrimSpace(*opts.XSD)
	}
	if req.Config == "" && req.ConfigXML == "" && opts.Config != nil {
		req.Config = strings.TrimSpace(*opts.Config)
	}
	if !req.Validate && opts.Validate != nil {
		req.Validate = *opts.Validate
	}
	if !req.Preflight && opts.Preflight != nil {
		req.Preflight = *opts.Preflight
	}
	if req.VarsRaw == "" && opts.Vars != nil {
		req.VarsRaw = strings.TrimSpace(*opts.Vars)
	}
	if !req.Debug && opts.Debug != nil {
		req.Debug = *opts.Debug
	}
	if req.GoPath == "" && opts.GoPath != nil {
		req.GoPath = strings.TrimSpace(*opts.GoPath)
	}
	if req.XSLT == "" && opts.XSLT != nil {
		req.XSLT = strings.TrimSpace(*opts.XSLT)
	}
	if req.Out == "" && opts.Out != nil {
		req.Out = strings.TrimSpace(*opts.Out)
	}
	if !req.NodeOutput && opts.NodeOutput != nil {
		req.NodeOutput = *opts.NodeOutput
	}
	if !req.Encrypted && opts.Encrypted != nil {
		req.Encrypted = *opts.Encrypted
	}
	if req.SecureKey == "" && opts.SecureKey != nil {
		req.SecureKey = strings.TrimSpace(*opts.SecureKey)
	}
	if !req.VerifySignature && opts.VerifySignature != nil {
		req.VerifySignature = *opts.VerifySignature
	}
	if req.PublicKey == "" && opts.PublicKey != nil {
		req.PublicKey = strings.TrimSpace(*opts.PublicKey)
	}
	if req.Cert == "" && opts.Cert != nil {
		req.Cert = strings.TrimSpace(*opts.Cert)
	}
	if req.CACert == "" && opts.CACert != nil {
		req.CACert = strings.TrimSpace(*opts.CACert)
	}
	if req.Keyring == "" && opts.Keyring != nil {
		req.Keyring = strings.TrimSpace(*opts.Keyring)
	}
	if req.Signature == "" && opts.Signature != nil {
		req.Signature = strings.TrimSpace(*opts.Signature)
	}

	return optLabel, nil
}

// ExecutePipeline executes or validates a pipeline with all available CLI/Builder options.
func (s *APIServer) ExecutePipeline(ctx context.Context, req ExecutionRequest, onEvent func(flow.ExecutionEvent)) (*ExecutionResponse, error) {
	start := time.Now().UTC()

	optionsLabel, err := s.applyOptionsXMLToRequest(ctx, &req)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.File) == "" && strings.TrimSpace(req.FileXML) == "" {
		req.File = "scripts.xml"
	}
	if strings.TrimSpace(req.Format) == "" {
		req.Format = "json"
	}

	pipelineBytes, fileLabel, err := s.resolveArtifactBytes(ctx, "pipeline", req.File, req.FileXML, &req)
	if err != nil {
		return nil, err
	}

	// Optional XSLT transformation
	if strings.TrimSpace(req.XSLT) != "" {
		xsltBytes, _, xsltErr := s.resolveArtifactBytes(ctx, "pipeline", req.XSLT, "", &req)
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
		return nil, fmt.Errorf("XML parsing error in pipeline %s: %w", fileLabel, err)
	}

	varConfigs := parsedCfg.Variables
	dbConfigs := parsedCfg.Databases
	preflightNodes := parsedCfg.PreflightNodes
	flowNodes := parsedCfg.FlowNodes

	var configLabel string
	if strings.TrimSpace(req.Config) != "" || strings.TrimSpace(req.ConfigXML) != "" {
		cfgBytes, lbl, err := s.resolveArtifactBytes(ctx, "config", req.Config, req.ConfigXML, &req)
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

	// Configure database logging sink (uses pipeline's log_db if defined, otherwise defaults to API startup DB)
	logDB := s.db
	logDialect := s.dialect
	var closeLogDB bool

	for i := len(dbConfigs) - 1; i >= 0; i-- {
		if dbConfigs[i].Name == "log_db" {
			if customDB, customDialect, openErr := OpenDatabase(dbConfigs[i].ConnectionString); openErr == nil {
				logDB = customDB
				logDialect = customDialect
				closeLogDB = true
			}
			break
		}
	}
	if closeLogDB {
		defer logDB.Close()
	}

	dbSink := NewDatabaseSink(logDB, logDialect, req.NodeOutput, req.Debug || s.cfg.Debug)
	var sinks []flow.EventSink
	sinks = append(sinks, dbSink)
	if onEvent != nil {
		sinks = append(sinks, &CallbackSink{OnEvent: onEvent})
	}
	executor.SetEventSink(&MultiSink{Sinks: sinks})

	scriptResults, execErr := executor.Execute(ctx, nodes)
	finishedAt := time.Now().UTC()

	if req.NodeOutput {
		_ = dbSink.RecordNodeOutputs(ctx, scriptResults)
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
		DBUserName:      dbSink.dbUser,
		SPID:            dbSink.spid,
		ApplicationName: dbSink.appName,
		Hostname:        hostname,
		OptionsPath:     optionsLabel,
		ErrorClass:      errClassStr,
		ErrorMessage:    errMsgStr,
	}
	_ = LogRunSummaryToDB(ctx, logDB, logDialect, summary)

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
