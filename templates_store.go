package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTemplateNotFound = errors.New("template not found")
	ErrTemplateExists   = errors.New("template already exists")
)

// TemplateRecord represents a reusable pipeline, options, or config XML template stored in TableTemplateContent.
type TemplateRecord struct {
	ID              int64     `json:"id,omitempty"`
	Type            string    `json:"type"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Content         string    `json:"content,omitempty"`
	CreatedBy       string    `json:"created_by"`
	UpdatedBy       string    `json:"updated_by"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	Encrypted       bool      `json:"encrypted"`
	Signed          bool      `json:"signed"`
	SignatureFormat string    `json:"signature_format,omitempty"`
}

// NormalizeTemplateType validates and normalizes a template type ("pipeline", "options", or "config").
func NormalizeTemplateType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pipeline", "pipelines", "script", "scripts":
		return "pipeline", nil
	case "options", "option":
		return "options", nil
	case "config", "configs", "configuration":
		return "config", nil
	default:
		return "", fmt.Errorf("unsupported template type %q: expected pipeline, options, or config", raw)
	}
}

func (s *APIServer) ListTemplates(ctx context.Context, typeFilter string, ownerFilter string, includeContent bool) ([]TemplateRecord, error) {
	tableName := resolveTableName(s.dialect, TableTemplateContent)

	selectCols := "ID, Type, Name, Description, CreatedBy, UpdatedBy, CreatedAt, UpdatedAt"
	if includeContent {
		selectCols = "ID, Type, Name, Description, TemplateXML, CreatedBy, UpdatedBy, CreatedAt, UpdatedAt"
	}

	var conds []string
	var args []any
	argIdx := 1

	if strings.TrimSpace(typeFilter) != "" {
		normType, err := NormalizeTemplateType(typeFilter)
		if err != nil {
			return nil, err
		}
		conds = append(conds, fmt.Sprintf("LOWER(Type) = %s", Placeholder(s.dialect, argIdx)))
		args = append(args, normType)
		argIdx++
	}
	if strings.TrimSpace(ownerFilter) != "" {
		conds = append(conds, fmt.Sprintf("LOWER(CreatedBy) = LOWER(%s)", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(ownerFilter))
		argIdx++
	}

	whereSQL := ""
	if len(conds) > 0 {
		whereSQL = " WHERE " + strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf("SELECT %s FROM %s%s ORDER BY UpdatedAt DESC, ID DESC", selectCols, tableName, whereSQL)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TemplateRecord
	for rows.Next() {
		var rec TemplateRecord
		var desc, content, createdBy, updatedBy sql.NullString
		var createdAtRaw, updatedAtRaw any

		if includeContent {
			if err := rows.Scan(&rec.ID, &rec.Type, &rec.Name, &desc, &content, &createdBy, &updatedBy, &createdAtRaw, &updatedAtRaw); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&rec.ID, &rec.Type, &rec.Name, &desc, &createdBy, &updatedBy, &createdAtRaw, &updatedAtRaw); err != nil {
				return nil, err
			}
		}

		rec.Description = desc.String
		rec.CreatedBy = strings.TrimSpace(createdBy.String)
		rec.UpdatedBy = strings.TrimSpace(updatedBy.String)
		if createdAtRaw != nil {
			rec.CreatedAt = parseDBTime(createdAtRaw)
		}
		if updatedAtRaw != nil {
			rec.UpdatedAt = parseDBTime(updatedAtRaw)
		}
		if includeContent {
			rec.Content = content.String
			rec.Encrypted, rec.Signed, rec.SignatureFormat = inspectArtifactSecurity(rec.Content)
		}
		out = append(out, rec)
	}
	if out == nil {
		out = []TemplateRecord{}
	}
	return out, rows.Err()
}

func (s *APIServer) GetTemplateByID(ctx context.Context, id int64) (*TemplateRecord, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid template id %d", id)
	}
	tableName := resolveTableName(s.dialect, TableTemplateContent)
	query := fmt.Sprintf(
		"SELECT ID, Type, Name, Description, TemplateXML, CreatedBy, UpdatedBy, CreatedAt, UpdatedAt FROM %s WHERE ID = %s",
		tableName, Placeholder(s.dialect, 1),
	)

	var rec TemplateRecord
	var desc, content, createdBy, updatedBy sql.NullString
	var createdAtRaw, updatedAtRaw any
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.Type, &rec.Name, &desc, &content, &createdBy, &updatedBy, &createdAtRaw, &updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	rec.Description = desc.String
	rec.Content = content.String
	rec.CreatedBy = strings.TrimSpace(createdBy.String)
	rec.UpdatedBy = strings.TrimSpace(updatedBy.String)
	if createdAtRaw != nil {
		rec.CreatedAt = parseDBTime(createdAtRaw)
	}
	if updatedAtRaw != nil {
		rec.UpdatedAt = parseDBTime(updatedAtRaw)
	}
	rec.Encrypted, rec.Signed, rec.SignatureFormat = inspectArtifactSecurity(rec.Content)
	return &rec, nil
}

func (s *APIServer) GetTemplateByTypeNameOwner(ctx context.Context, tmplType, name, owner string) (*TemplateRecord, error) {
	normType, err := NormalizeTemplateType(tmplType)
	if err != nil {
		return nil, err
	}
	tableName := resolveTableName(s.dialect, TableTemplateContent)
	query := fmt.Sprintf(
		"SELECT ID, Type, Name, Description, TemplateXML, CreatedBy, UpdatedBy, CreatedAt, UpdatedAt FROM %s WHERE LOWER(Type) = %s AND Name = %s AND LOWER(CreatedBy) = LOWER(%s)",
		tableName, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2), Placeholder(s.dialect, 3),
	)

	var rec TemplateRecord
	var desc, content, createdBy, updatedBy sql.NullString
	var createdAtRaw, updatedAtRaw any
	err = s.db.QueryRowContext(ctx, query, normType, strings.TrimSpace(name), strings.TrimSpace(owner)).Scan(
		&rec.ID, &rec.Type, &rec.Name, &desc, &content, &createdBy, &updatedBy, &createdAtRaw, &updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	rec.Description = desc.String
	rec.Content = content.String
	rec.CreatedBy = strings.TrimSpace(createdBy.String)
	rec.UpdatedBy = strings.TrimSpace(updatedBy.String)
	if createdAtRaw != nil {
		rec.CreatedAt = parseDBTime(createdAtRaw)
	}
	if updatedAtRaw != nil {
		rec.UpdatedAt = parseDBTime(updatedAtRaw)
	}
	rec.Encrypted, rec.Signed, rec.SignatureFormat = inspectArtifactSecurity(rec.Content)
	return &rec, nil
}

func (s *APIServer) CreateOrUpsertTemplate(ctx context.Context, rec TemplateRecord, overwrite bool) (*TemplateRecord, error) {
	normType, err := NormalizeTemplateType(rec.Type)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(rec.Name)
	if name == "" {
		return nil, fmt.Errorf("template name is required")
	}
	owner := strings.TrimSpace(rec.CreatedBy)
	updatedBy := strings.TrimSpace(rec.UpdatedBy)
	if updatedBy == "" {
		updatedBy = owner
	}

	existing, err := s.GetTemplateByTypeNameOwner(ctx, normType, name, owner)
	if err != nil && !errors.Is(err, ErrTemplateNotFound) {
		return nil, err
	}
	if existing != nil {
		if !overwrite {
			return nil, ErrTemplateExists
		}
		rec.Type = normType
		rec.Name = name
		rec.UpdatedBy = updatedBy
		return s.UpdateTemplateByID(ctx, existing.ID, rec)
	}

	now := time.Now().UTC()
	tableName := resolveTableName(s.dialect, TableTemplateContent)
	query := fmt.Sprintf(
		"INSERT INTO %s (Type, Name, Description, TemplateXML, CreatedBy, UpdatedBy, CreatedAt, UpdatedAt) VALUES (%s, %s, %s, %s, %s, %s, %s, %s)",
		tableName,
		Placeholder(s.dialect, 1),
		Placeholder(s.dialect, 2),
		Placeholder(s.dialect, 3),
		Placeholder(s.dialect, 4),
		Placeholder(s.dialect, 5),
		Placeholder(s.dialect, 6),
		Placeholder(s.dialect, 7),
		Placeholder(s.dialect, 8),
	)
	if _, err := s.db.ExecContext(ctx, query, normType, name, rec.Description, rec.Content, owner, updatedBy, now, now); err != nil {
		return nil, err
	}
	return s.GetTemplateByTypeNameOwner(ctx, normType, name, owner)
}

func (s *APIServer) UpdateTemplateByID(ctx context.Context, id int64, rec TemplateRecord) (*TemplateRecord, error) {
	existing, err := s.GetTemplateByID(ctx, id)
	if err != nil {
		return nil, err
	}

	normType := existing.Type
	if strings.TrimSpace(rec.Type) != "" {
		normType, err = NormalizeTemplateType(rec.Type)
		if err != nil {
			return nil, err
		}
	}
	name := strings.TrimSpace(rec.Name)
	if name == "" {
		name = existing.Name
	}
	desc := rec.Description
	content := rec.Content
	if content == "" {
		content = existing.Content
	}
	updatedBy := strings.TrimSpace(rec.UpdatedBy)
	if updatedBy == "" {
		updatedBy = existing.UpdatedBy
	}

	// Ensure rename doesn't collide with another template owned by the same author
	if !strings.EqualFold(name, existing.Name) || !strings.EqualFold(normType, existing.Type) {
		if conflict, cErr := s.GetTemplateByTypeNameOwner(ctx, normType, name, existing.CreatedBy); cErr == nil && conflict != nil && conflict.ID != id {
			return nil, ErrTemplateExists
		}
	}

	now := time.Now().UTC()
	tableName := resolveTableName(s.dialect, TableTemplateContent)
	query := fmt.Sprintf(
		"UPDATE %s SET Type = %s, Name = %s, Description = %s, TemplateXML = %s, UpdatedBy = %s, UpdatedAt = %s WHERE ID = %s",
		tableName,
		Placeholder(s.dialect, 1),
		Placeholder(s.dialect, 2),
		Placeholder(s.dialect, 3),
		Placeholder(s.dialect, 4),
		Placeholder(s.dialect, 5),
		Placeholder(s.dialect, 6),
		Placeholder(s.dialect, 7),
	)
	if _, err := s.db.ExecContext(ctx, query, normType, name, desc, content, updatedBy, now, id); err != nil {
		return nil, err
	}
	return s.GetTemplateByID(ctx, id)
}

func (s *APIServer) DeleteTemplateByID(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("invalid template id %d", id)
	}
	tableName := resolveTableName(s.dialect, TableTemplateContent)
	query := fmt.Sprintf("DELETE FROM %s WHERE ID = %s", tableName, Placeholder(s.dialect, 1))
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}
