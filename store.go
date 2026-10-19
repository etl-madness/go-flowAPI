package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/etl-madness/go-flow/pkg/flowcrypto"
)

var ErrArtifactNotFound = errors.New("artifact not found")
var ErrArtifactExists = errors.New("artifact already exists")

// ArtifactRecord represents a pipeline, options, or config XML document stored in the database.
type ArtifactRecord struct {
	ID          int64  `json:"id,omitempty"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content,omitempty"`
	Encrypted   bool   `json:"encrypted"`
}

// ResolveArtifactTarget maps an artifact type ("pipeline", "options", "config")
// to its adjustable table constant and XML content column name.
func ResolveArtifactTarget(artifactType string) (tableConst string, xmlColumn string, canonicalType string, err error) {
	switch strings.ToLower(strings.TrimSpace(artifactType)) {
	case "pipeline", "pipelines", "script", "scripts", "":
		return TablePipelineContent, "PipelineXML", "pipeline", nil
	case "options", "option":
		return TableOptionsContent, "OptionsXML", "options", nil
	case "config", "configs", "configuration":
		return TableConfigContent, "ConfigXML", "config", nil
	default:
		return "", "", "", fmt.Errorf("unsupported artifact type %q: expected pipeline, options, or config", artifactType)
	}
}

// DecodeUploadedBytes strips UTF-8 BOM and converts UTF-16LE/BE byte streams to UTF-8 string
// (matching db_importer/mssql_importer.go behavior).
func DecodeUploadedBytes(data []byte) string {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return string(data[3:])
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE && len(data)%2 == 0 {
			codeUnits := make([]uint16, 0, len(data)/2-1)
			for i := 2; i+1 < len(data); i += 2 {
				codeUnits = append(codeUnits, uint16(data[i])|uint16(data[i+1])<<8)
			}
			return string(utf16.Decode(codeUnits))
		}
		if data[0] == 0xFE && data[1] == 0xFF && len(data)%2 == 0 {
			codeUnits := make([]uint16, 0, len(data)/2-1)
			for i := 2; i+1 < len(data); i += 2 {
				codeUnits = append(codeUnits, uint16(data[i+1])|uint16(data[i])<<8)
			}
			return string(utf16.Decode(codeUnits))
		}
	}
	return string(data)
}

func buildArtifactUpsertQuery(dialect, tableName, xmlColumn string) string {
	switch strings.ToLower(dialect) {
	case "sqlserver", "azuresql":
		return fmt.Sprintf(`
			MERGE INTO %s AS target
			USING (SELECT @p1 AS Name) AS source
			ON (target.Name = source.Name)
			WHEN MATCHED THEN
				UPDATE SET Description = @p2, %s = @p3
			WHEN NOT MATCHED THEN
				INSERT (Name, Description, %s) VALUES (@p1, @p2, @p3);
		`, tableName, xmlColumn, xmlColumn)
	case "postgres":
		return fmt.Sprintf(`
			INSERT INTO %s (Name, Description, %s) VALUES ($1, $2, $3)
			ON CONFLICT (Name) DO UPDATE SET Description = EXCLUDED.Description, %s = EXCLUDED.%s;
		`, tableName, xmlColumn, xmlColumn, xmlColumn)
	case "mysql":
		return fmt.Sprintf(`
			INSERT INTO %s (Name, Description, %s) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE Description = VALUES(Description), %s = VALUES(%s);
		`, tableName, xmlColumn, xmlColumn, xmlColumn)
	case "oracle":
		return fmt.Sprintf(`
			MERGE INTO %s target
			USING (SELECT :1 AS Name FROM DUAL) source
			ON (target.Name = source.Name)
			WHEN MATCHED THEN
				UPDATE SET target.Description = :2, target.%s = :3
			WHEN NOT MATCHED THEN
				INSERT (Name, Description, %s) VALUES (:1, :2, :3)
		`, tableName, xmlColumn, xmlColumn)
	case "sqlite", "sqlite3":
		fallthrough
	default:
		return fmt.Sprintf(`
			INSERT INTO %s (Name, Description, %s) VALUES (?, ?, ?)
			ON CONFLICT(Name) DO UPDATE SET Description = excluded.Description, %s = excluded.%s;
		`, tableName, xmlColumn, xmlColumn, xmlColumn)
	}
}

// UpsertArtifact inserts or updates an artifact record in the database across all supported dialects.
func (s *APIServer) UpsertArtifact(ctx context.Context, artifactType, name, desc, rawContent string, encrypt bool, secureKey string) (*ArtifactRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("artifact name is required")
	}

	tableConst, xmlCol, canonicalType, err := ResolveArtifactTarget(artifactType)
	if err != nil {
		return nil, err
	}
	table := resolveTableName(s.dialect, tableConst)

	contentToStore := rawContent
	if encrypt && !flowcrypto.IsEncryptedPayload([]byte(rawContent)) {
		key := strings.TrimSpace(secureKey)
		if key == "" {
			key = s.cfg.DefaultSecureKey
		}
		if key == "" {
			return nil, fmt.Errorf("encryption requested, but no secure_key was provided and FLOW_SECURE_KEY is not configured")
		}
		armored, err := flowcrypto.EncryptArmored([]byte(rawContent), key)
		if err != nil {
			return nil, fmt.Errorf("failed encrypting artifact content: %w", err)
		}
		contentToStore = armored
	}

	query := buildArtifactUpsertQuery(s.dialect, table, xmlCol)
	if _, err := s.db.ExecContext(ctx, query, name, desc, contentToStore); err != nil {
		return nil, fmt.Errorf("failed upserting %s %q into %s: %w", canonicalType, name, table, err)
	}

	var id int64
	idQuery := fmt.Sprintf("SELECT ID FROM %s WHERE Name = %s", table, Placeholder(s.dialect, 1))
	_ = s.db.QueryRowContext(ctx, idQuery, name).Scan(&id)

	return &ArtifactRecord{
		ID:          id,
		Type:        canonicalType,
		Name:        name,
		Description: desc,
		Content:     contentToStore,
		Encrypted:   flowcrypto.IsEncryptedPayload([]byte(contentToStore)),
	}, nil
}

// CreateArtifact inserts a new artifact and returns ErrArtifactExists if the name already exists.
func (s *APIServer) CreateArtifact(ctx context.Context, artifactType, name, desc, rawContent string, encrypt bool, secureKey string) (*ArtifactRecord, error) {
	existing, err := s.GetArtifact(ctx, artifactType, name, false, "")
	if err == nil && existing != nil {
		return nil, ErrArtifactExists
	}
	if err != nil && !errors.Is(err, ErrArtifactNotFound) {
		return nil, err
	}
	return s.UpsertArtifact(ctx, artifactType, name, desc, rawContent, encrypt, secureKey)
}

// UpdateArtifact updates an existing artifact and returns ErrArtifactNotFound if the name does not exist.
func (s *APIServer) UpdateArtifact(ctx context.Context, artifactType, name, desc, rawContent string, encrypt bool, secureKey string) (*ArtifactRecord, error) {
	existing, err := s.GetArtifact(ctx, artifactType, name, false, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(desc) == "" {
		desc = existing.Description
	}
	return s.UpsertArtifact(ctx, artifactType, name, desc, rawContent, encrypt, secureKey)
}

// UpdateArtifactByID updates an existing artifact by its database table ID and returns ErrArtifactNotFound if the ID does not exist.
func (s *APIServer) UpdateArtifactByID(ctx context.Context, artifactType string, id int64, desc, rawContent string, encrypt bool, secureKey string) (*ArtifactRecord, error) {
	existing, err := s.GetArtifactByID(ctx, artifactType, id, false, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(desc) == "" {
		desc = existing.Description
	}
	return s.UpsertArtifact(ctx, artifactType, existing.Name, desc, rawContent, encrypt, secureKey)
}

func (s *APIServer) finalizeArtifactRecord(rec *ArtifactRecord, table string, decrypt bool, secureKey string) (*ArtifactRecord, error) {
	isEnc := flowcrypto.IsEncryptedPayload([]byte(rec.Content))
	rec.Encrypted = isEnc

	if decrypt && isEnc {
		key := strings.TrimSpace(secureKey)
		if key == "" {
			key = s.cfg.DefaultSecureKey
		}
		if key == "" {
			return nil, fmt.Errorf("artifact %q (id=%d) in %s is encrypted, but no secure_key was provided", rec.Name, rec.ID, table)
		}
		plain, err := flowcrypto.DecryptAuto([]byte(rec.Content), key, true)
		if err != nil {
			return nil, fmt.Errorf("failed decrypting artifact %q (id=%d): %w", rec.Name, rec.ID, err)
		}
		rec.Content = string(plain)
		rec.Encrypted = false
	}

	return rec, nil
}

// GetArtifact fetches a single artifact by name, optionally decrypting its content if encrypted.
func (s *APIServer) GetArtifact(ctx context.Context, artifactType, name string, decrypt bool, secureKey string) (*ArtifactRecord, error) {
	name = strings.TrimSpace(name)
	tableConst, xmlCol, canonicalType, err := ResolveArtifactTarget(artifactType)
	if err != nil {
		return nil, err
	}
	table := resolveTableName(s.dialect, tableConst)
	ph := Placeholder(s.dialect, 1)

	query := fmt.Sprintf("SELECT ID, Name, COALESCE(Description, ''), COALESCE(%s, '') FROM %s WHERE Name = %s", xmlCol, table, ph)
	var rec ArtifactRecord
	rec.Type = canonicalType

	err = s.db.QueryRowContext(ctx, query, name).Scan(&rec.ID, &rec.Name, &rec.Description, &rec.Content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactNotFound
		}
		return nil, fmt.Errorf("failed querying %s for %q: %w", table, name, err)
	}

	return s.finalizeArtifactRecord(&rec, table, decrypt, secureKey)
}

// GetArtifactByID fetches a single artifact by its database table ID primary key, optionally decrypting its content if encrypted.
func (s *APIServer) GetArtifactByID(ctx context.Context, artifactType string, id int64, decrypt bool, secureKey string) (*ArtifactRecord, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid artifact ID %d: must be a positive integer", id)
	}
	tableConst, xmlCol, canonicalType, err := ResolveArtifactTarget(artifactType)
	if err != nil {
		return nil, err
	}
	table := resolveTableName(s.dialect, tableConst)
	ph := Placeholder(s.dialect, 1)

	query := fmt.Sprintf("SELECT ID, Name, COALESCE(Description, ''), COALESCE(%s, '') FROM %s WHERE ID = %s", xmlCol, table, ph)
	var rec ArtifactRecord
	rec.Type = canonicalType

	err = s.db.QueryRowContext(ctx, query, id).Scan(&rec.ID, &rec.Name, &rec.Description, &rec.Content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactNotFound
		}
		return nil, fmt.Errorf("failed querying %s for ID %d: %w", table, id, err)
	}

	return s.finalizeArtifactRecord(&rec, table, decrypt, secureKey)
}

// ListArtifacts returns all artifacts of the given type from the database.
func (s *APIServer) ListArtifacts(ctx context.Context, artifactType string, includeContent bool) ([]ArtifactRecord, error) {
	tableConst, xmlCol, canonicalType, err := ResolveArtifactTarget(artifactType)
	if err != nil {
		return nil, err
	}
	table := resolveTableName(s.dialect, tableConst)

	query := fmt.Sprintf("SELECT ID, Name, COALESCE(Description, ''), COALESCE(%s, '') FROM %s ORDER BY Name ASC", xmlCol, table)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed listing %s from %s: %w", canonicalType, table, err)
	}
	defer rows.Close()

	var items []ArtifactRecord
	for rows.Next() {
		var rec ArtifactRecord
		rec.Type = canonicalType
		var content string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Description, &content); err != nil {
			return nil, err
		}
		rec.Encrypted = flowcrypto.IsEncryptedPayload([]byte(content))
		if includeContent {
			rec.Content = content
		}
		items = append(items, rec)
	}
	if items == nil {
		items = []ArtifactRecord{}
	}
	return items, rows.Err()
}

// DeleteArtifact deletes an artifact by name and returns ErrArtifactNotFound if no row was deleted.
func (s *APIServer) DeleteArtifact(ctx context.Context, artifactType, name string) error {
	name = strings.TrimSpace(name)
	tableConst, _, _, err := ResolveArtifactTarget(artifactType)
	if err != nil {
		return err
	}
	table := resolveTableName(s.dialect, tableConst)
	ph := Placeholder(s.dialect, 1)

	query := fmt.Sprintf("DELETE FROM %s WHERE Name = %s", table, ph)
	res, err := s.db.ExecContext(ctx, query, name)
	if err != nil {
		return fmt.Errorf("failed deleting %q from %s: %w", name, table, err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return ErrArtifactNotFound
	}
	return nil
}

// DeleteArtifactByID deletes an artifact by its database table ID and returns ErrArtifactNotFound if no row was deleted.
func (s *APIServer) DeleteArtifactByID(ctx context.Context, artifactType string, id int64) error {
	if id <= 0 {
		return fmt.Errorf("invalid artifact ID %d: must be a positive integer", id)
	}
	tableConst, _, _, err := ResolveArtifactTarget(artifactType)
	if err != nil {
		return err
	}
	table := resolveTableName(s.dialect, tableConst)
	ph := Placeholder(s.dialect, 1)

	query := fmt.Sprintf("DELETE FROM %s WHERE ID = %s", table, ph)
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed deleting ID %d from %s: %w", id, table, err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return ErrArtifactNotFound
	}
	return nil
}

// UpsertAPIUser creates or updates a BasicAuth user in TableAPIUsers (requires "all" permission when called via API).
func (s *APIServer) UpsertAPIUser(ctx context.Context, username, rawPassword, permission, description string, isActive bool) (*APIUser, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if err := ValidatePermissionString(permission); err != nil {
		return nil, err
	}

	var passwordHash string
	if strings.HasPrefix(rawPassword, "$2a$") || strings.HasPrefix(rawPassword, "$2b$") || strings.HasPrefix(strings.ToLower(rawPassword), "sha256:") {
		passwordHash = rawPassword
	} else {
		var err error
		passwordHash, err = HashPasswordBcrypt(rawPassword)
		if err != nil {
			return nil, fmt.Errorf("failed hashing password: %w", err)
		}
	}

	table := resolveTableName(s.dialect, TableAPIUsers)
	activeInt := 0
	if isActive {
		activeInt = 1
	}

	// Delete + Insert within a transaction or dialect upsert for portable user management
	delQuery := fmt.Sprintf("DELETE FROM %s WHERE username = %s", table, Placeholder(s.dialect, 1))
	_, _ = s.db.ExecContext(ctx, delQuery, username)

	insQuery := BuildInsertQuery(s.dialect, TableAPIUsers, []string{"username", "password_hash", "permission", "is_active", "description"})
	if _, err := s.db.ExecContext(ctx, insQuery, username, passwordHash, permission, activeInt, description); err != nil {
		return nil, fmt.Errorf("failed saving user %q in %s: %w", username, table, err)
	}

	return &APIUser{
		Username:    username,
		Permission:  permission,
		IsActive:    isActive,
		Description: description,
	}, nil
}

// ListAPIUsers lists all users in TableAPIUsers (excluding password hashes).
func (s *APIServer) ListAPIUsers(ctx context.Context) ([]APIUser, error) {
	table := resolveTableName(s.dialect, TableAPIUsers)
	query := fmt.Sprintf("SELECT username, permission, is_active, COALESCE(description, '') FROM %s ORDER BY username ASC", table)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []APIUser
	for rows.Next() {
		var u APIUser
		var activeRaw any
		if err := rows.Scan(&u.Username, &u.Permission, &activeRaw, &u.Description); err != nil {
			return nil, err
		}
		u.IsActive = parseDBBool(activeRaw)
		users = append(users, u)
	}
	if users == nil {
		users = []APIUser{}
	}
	return users, rows.Err()
}

// DeleteAPIUser deletes a user from TableAPIUsers.
func (s *APIServer) DeleteAPIUser(ctx context.Context, username string) error {
	table := resolveTableName(s.dialect, TableAPIUsers)
	query := fmt.Sprintf("DELETE FROM %s WHERE username = %s", table, Placeholder(s.dialect, 1))
	res, err := s.db.ExecContext(ctx, query, strings.TrimSpace(username))
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrArtifactNotFound
	}
	return nil
}
