package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Granular Pipeline ACL Constants
const (
	ActionCreate  = "create"
	ActionRead    = "read"
	ActionUpdate  = "update"
	ActionDelete  = "delete"
	ActionExecute = "execute"
	ActionAll     = "all"

	PrincipalUser  = "user"
	PrincipalGroup = "group"

	TargetPipeline      = "pipeline"
	TargetPipelineGroup = "pipeline_group"
	TargetWildcard      = "*"

	EffectAllow = "allow"
	EffectDeny  = "deny"
)

var canonicalActionOrder = []string{
	ActionCreate,
	ActionRead,
	ActionUpdate,
	ActionDelete,
	ActionExecute,
}

// ErrPipelineForbidden indicates the authenticated principal lacks permission for the requested pipeline action.
var ErrPipelineForbidden = errors.New("pipeline access forbidden")

// APIGroup represents a logical user group in TableAPIGroups (dbo.flow_api_groups).
type APIGroup struct {
	ID          int64     `json:"id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Members     []string  `json:"members,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// PipelineGroup represents a logical collection of pipelines in TablePipelineGroups (dbo.flow_pipeline_groups).
type PipelineGroup struct {
	ID          int64     `json:"id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	NamePattern string    `json:"name_pattern,omitempty"`
	Pipelines   []string  `json:"pipelines,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// PipelineACL represents a granular allow/deny access rule in TablePipelineACL (dbo.flow_pipeline_acl).
type PipelineACL struct {
	ID            int64     `json:"id"`
	PrincipalType string    `json:"principal_type"`
	PrincipalName string    `json:"principal_name"`
	TargetType    string    `json:"target_type"`
	TargetName    string    `json:"target_name"`
	Permissions   string    `json:"permissions"`
	Effect        string    `json:"effect"`
	Description   string    `json:"description,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// EffectivePipelinePermission summarizes resolved permissions for a principal on a single pipeline.
type EffectivePipelinePermission struct {
	PipelineID     int64    `json:"pipeline_id,omitempty"`
	PipelineName   string   `json:"pipeline_name"`
	Groups         []string `json:"groups,omitempty"`
	AllowedActions []string `json:"allowed_actions"`
	DeniedActions  []string `json:"denied_actions,omitempty"`
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "invalid object name") ||
		(strings.Contains(msg, "relation ") && strings.Contains(msg, "does not exist")) ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "ora-00942") ||
		strings.Contains(msg, "table or view does not exist")
}

// NormalizeACLActions validates and canonicalizes a comma-separated granular permissions string.
func NormalizeACLActions(rawPerms string) (string, map[string]bool, error) {
	if strings.TrimSpace(rawPerms) == "" {
		return "", nil, fmt.Errorf("permissions cannot be empty; expected create, read, update, delete, execute, or all")
	}
	granted := make(map[string]bool, 5)
	hasAll := false

	for _, tok := range strings.Split(rawPerms, ",") {
		t := strings.ToLower(strings.TrimSpace(tok))
		if t == "" {
			continue
		}
		switch t {
		case "all", "*", "full", "admin":
			hasAll = true
			for _, a := range canonicalActionOrder {
				granted[a] = true
			}
		case "create", "insert", "new":
			granted[ActionCreate] = true
		case "read", "view", "get", "list", "export", "read-only", "read_only":
			granted[ActionRead] = true
		case "update", "edit", "modify", "put":
			granted[ActionUpdate] = true
		case "delete", "remove", "drop":
			granted[ActionDelete] = true
		case "execute", "exec", "run", "stream", "validate", "execution-only", "execution_only":
			granted[ActionExecute] = true
		case "write", "crud":
			granted[ActionCreate] = true
			granted[ActionRead] = true
			granted[ActionUpdate] = true
			granted[ActionDelete] = true
		case "upload":
			granted[ActionCreate] = true
			granted[ActionRead] = true
			granted[ActionUpdate] = true
		default:
			return "", nil, fmt.Errorf("unsupported ACL permission %q: expected create, read, update, delete, execute, or all", strings.TrimSpace(tok))
		}
	}

	if len(granted) == 0 {
		return "", nil, fmt.Errorf("permissions cannot be empty; expected create, read, update, delete, execute, or all")
	}
	if hasAll {
		return ActionAll, granted, nil
	}

	var ordered []string
	for _, a := range canonicalActionOrder {
		if granted[a] {
			ordered = append(ordered, a)
		}
	}
	return strings.Join(ordered, ","), granted, nil
}

func aclGrantsAction(perms string, action string) bool {
	_, granted, err := NormalizeACLActions(perms)
	if err != nil {
		return false
	}
	return granted[strings.ToLower(strings.TrimSpace(action))]
}

func normalizePrincipalType(pt string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(pt)) {
	case "user", "users":
		return PrincipalUser, nil
	case "group", "groups", "ad_group", "role":
		return PrincipalGroup, nil
	default:
		return "", fmt.Errorf("unsupported principal_type %q: expected 'user' or 'group'", pt)
	}
}

func normalizeTargetType(tt string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(tt)) {
	case "pipeline", "pipelines":
		return TargetPipeline, nil
	case "pipeline_group", "pipeline-group", "pipelinegroup", "group":
		return TargetPipelineGroup, nil
	case "*", "all", "global", "wildcard":
		return TargetWildcard, nil
	default:
		return "", fmt.Errorf("unsupported target_type %q: expected 'pipeline', 'pipeline_group', or '*'", tt)
	}
}

func normalizeEffect(eff string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(eff))
	if s == "" || s == "allow" || s == "permit" {
		return EffectAllow, nil
	}
	if s == "deny" || s == "block" {
		return EffectDeny, nil
	}
	return "", fmt.Errorf("unsupported effect %q: expected 'allow' or 'deny'", eff)
}

func matchPipelinePattern(pattern, pipelineName string) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	n := strings.ToLower(strings.TrimSpace(pipelineName))
	if p == "" || n == "" {
		return false
	}
	matched, err := path.Match(p, n)
	return err == nil && matched
}

// =============================================================================
// 1. User Group Store Operations (TableAPIGroups & TableAPIUserGroups)
// =============================================================================

// ListGroupsForUser returns all local API groups assigned to username (or short username for DOMAIN\user).
// Returns nil without error if TableAPIUserGroups has not been provisioned yet.
func (s *APIServer) ListGroupsForUser(ctx context.Context, username string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, nil
	}

	shortUser := username
	if idx := strings.LastIndex(username, `\`); idx >= 0 && idx+1 < len(username) {
		shortUser = username[idx+1:]
	}

	table := resolveTableName(s.dialect, TableAPIUserGroups)
	var rows *sql.Rows
	var err error
	if !strings.EqualFold(shortUser, username) {
		query := fmt.Sprintf(
			"SELECT group_name FROM %s WHERE LOWER(username) = LOWER(%s) OR LOWER(username) = LOWER(%s) ORDER BY group_name ASC",
			table, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2),
		)
		rows, err = s.db.QueryContext(ctx, query, username, shortUser)
	} else {
		query := fmt.Sprintf(
			"SELECT group_name FROM %s WHERE LOWER(username) = LOWER(%s) ORDER BY group_name ASC",
			table, Placeholder(s.dialect, 1),
		)
		rows, err = s.db.QueryContext(ctx, query, username)
	}
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var groups []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		key := strings.ToLower(g)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			groups = append(groups, g)
		}
	}
	return groups, rows.Err()
}

// HasWindowsACLOrGroupRegistration checks whether a Windows/AD user or any of their groups
// appears in TableAPIUserGroups or TablePipelineACL, allowing authentication as a 'scoped' principal
// without requiring a separate row in TableAPIUsers.
func (s *APIServer) HasWindowsACLOrGroupRegistration(ctx context.Context, windowsUser, shortUser string, groupSet map[string]struct{}) (bool, []string) {
	if s == nil || s.db == nil {
		return false, nil
	}
	var matched []string
	seen := make(map[string]struct{})
	addMatch := func(label string) {
		k := strings.ToLower(label)
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			matched = append(matched, label)
		}
	}

	if dbGroups, err := s.ListGroupsForUser(ctx, windowsUser); err == nil && len(dbGroups) > 0 {
		for _, g := range dbGroups {
			addMatch("group:" + g)
			groupSet[strings.ToLower(g)] = struct{}{}
		}
	}

	aclTable := resolveTableName(s.dialect, TablePipelineACL)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT principal_type, principal_name FROM %s", aclTable))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pType, pName string
			if scanErr := rows.Scan(&pType, &pName); scanErr != nil {
				continue
			}
			pType = strings.ToLower(strings.TrimSpace(pType))
			pName = strings.TrimSpace(pName)
			if pType == PrincipalUser {
				if strings.EqualFold(pName, windowsUser) || strings.EqualFold(pName, shortUser) {
					addMatch(pName)
				}
			} else if pType == PrincipalGroup {
				if _, ok := groupSet[strings.ToLower(pName)]; ok {
					addMatch(pName)
				}
			}
		}
	}

	return len(matched) > 0, matched
}

// UpsertAPIGroup creates or updates a user group in TableAPIGroups and optionally adds initial members.
func (s *APIServer) UpsertAPIGroup(ctx context.Context, name, description string, members []string) (*APIGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}

	table := resolveTableName(s.dialect, TableAPIGroups)
	var existingID int64
	checkQuery := fmt.Sprintf("SELECT id FROM %s WHERE LOWER(name) = LOWER(%s)", table, Placeholder(s.dialect, 1))
	err := s.db.QueryRowContext(ctx, checkQuery, name).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed querying %s: %w", table, err)
	}

	if err == nil && existingID > 0 {
		updQuery := fmt.Sprintf("UPDATE %s SET description = %s WHERE id = %s", table, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2))
		if _, err := s.db.ExecContext(ctx, updQuery, description, existingID); err != nil {
			return nil, fmt.Errorf("failed updating group %q in %s: %w", name, table, err)
		}
	} else {
		insQuery := BuildInsertQuery(s.dialect, TableAPIGroups, []string{"name", "description"})
		if _, err := s.db.ExecContext(ctx, insQuery, name, description); err != nil {
			return nil, fmt.Errorf("failed creating group %q in %s: %w", name, table, err)
		}
	}

	for _, m := range members {
		if trimmed := strings.TrimSpace(m); trimmed != "" {
			if err := s.AddUserToGroup(ctx, name, trimmed); err != nil {
				return nil, err
			}
		}
	}

	return s.GetAPIGroup(ctx, name)
}

// GetAPIGroup retrieves a single API group and its member usernames.
func (s *APIServer) GetAPIGroup(ctx context.Context, name string) (*APIGroup, error) {
	name = strings.TrimSpace(name)
	table := resolveTableName(s.dialect, TableAPIGroups)
	query := fmt.Sprintf("SELECT id, name, COALESCE(description, '') FROM %s WHERE LOWER(name) = LOWER(%s)", table, Placeholder(s.dialect, 1))

	var grp APIGroup
	if err := s.db.QueryRowContext(ctx, query, name).Scan(&grp.ID, &grp.Name, &grp.Description); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactNotFound
		}
		return nil, err
	}

	memTable := resolveTableName(s.dialect, TableAPIUserGroups)
	memQuery := fmt.Sprintf("SELECT username FROM %s WHERE LOWER(group_name) = LOWER(%s) ORDER BY username ASC", memTable, Placeholder(s.dialect, 1))
	rows, err := s.db.QueryContext(ctx, memQuery, grp.Name)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err == nil {
				grp.Members = append(grp.Members, u)
			}
		}
	}
	if grp.Members == nil {
		grp.Members = []string{}
	}
	return &grp, nil
}

// ListAPIGroups lists all user groups and their members.
func (s *APIServer) ListAPIGroups(ctx context.Context) ([]APIGroup, error) {
	table := resolveTableName(s.dialect, TableAPIGroups)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT id, name, COALESCE(description, '') FROM %s ORDER BY name ASC", table))
	if err != nil {
		if isMissingTableError(err) {
			return []APIGroup{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	var groups []APIGroup
	idxByLower := make(map[string]int)
	for rows.Next() {
		var g APIGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Description); err != nil {
			return nil, err
		}
		g.Members = []string{}
		idxByLower[strings.ToLower(g.Name)] = len(groups)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if groups == nil {
		return []APIGroup{}, nil
	}

	memTable := resolveTableName(s.dialect, TableAPIUserGroups)
	mRows, mErr := s.db.QueryContext(ctx, fmt.Sprintf("SELECT group_name, username FROM %s ORDER BY username ASC", memTable))
	if mErr == nil {
		defer mRows.Close()
		for mRows.Next() {
			var gName, uName string
			if err := mRows.Scan(&gName, &uName); err == nil {
				if idx, ok := idxByLower[strings.ToLower(strings.TrimSpace(gName))]; ok {
					groups[idx].Members = append(groups[idx].Members, uName)
				}
			}
		}
	}
	return groups, nil
}

// DeleteAPIGroup deletes a user group and cleans up its memberships and ACL rules.
func (s *APIServer) DeleteAPIGroup(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	table := resolveTableName(s.dialect, TableAPIGroups)
	res, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(name) = LOWER(%s)", table, Placeholder(s.dialect, 1)), name)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrArtifactNotFound
	}

	memTable := resolveTableName(s.dialect, TableAPIUserGroups)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(group_name) = LOWER(%s)", memTable, Placeholder(s.dialect, 1)), name)

	aclTable := resolveTableName(s.dialect, TablePipelineACL)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(principal_type) = 'group' AND LOWER(principal_name) = LOWER(%s)", aclTable, Placeholder(s.dialect, 1)), name)
	return nil
}

// AddUserToGroup adds a username to an API group (idempotent).
func (s *APIServer) AddUserToGroup(ctx context.Context, groupName, username string) error {
	groupName = strings.TrimSpace(groupName)
	username = strings.TrimSpace(username)
	if groupName == "" || username == "" {
		return fmt.Errorf("group_name and username are required")
	}

	grp, err := s.GetAPIGroup(ctx, groupName)
	if err != nil {
		return fmt.Errorf("group %q not found: %w", groupName, err)
	}

	memTable := resolveTableName(s.dialect, TableAPIUserGroups)
	checkQuery := fmt.Sprintf(
		"SELECT username FROM %s WHERE LOWER(group_name) = LOWER(%s) AND LOWER(username) = LOWER(%s)",
		memTable, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2),
	)
	var existing string
	if err := s.db.QueryRowContext(ctx, checkQuery, grp.Name, username).Scan(&existing); err == nil {
		return nil // already a member
	}

	insQuery := BuildInsertQuery(s.dialect, TableAPIUserGroups, []string{"group_name", "username"})
	_, err = s.db.ExecContext(ctx, insQuery, grp.Name, username)
	return err
}

// RemoveUserFromGroup removes a username from an API group.
func (s *APIServer) RemoveUserFromGroup(ctx context.Context, groupName, username string) error {
	groupName = strings.TrimSpace(groupName)
	username = strings.TrimSpace(username)
	memTable := resolveTableName(s.dialect, TableAPIUserGroups)
	query := fmt.Sprintf(
		"DELETE FROM %s WHERE LOWER(group_name) = LOWER(%s) AND LOWER(username) = LOWER(%s)",
		memTable, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2),
	)
	res, err := s.db.ExecContext(ctx, query, groupName, username)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrArtifactNotFound
	}
	return nil
}

// =============================================================================
// 2. Pipeline Group Store Operations (TablePipelineGroups & TablePipelineGroupMembers)
// =============================================================================

// UpsertPipelineGroup creates or updates a pipeline group and optionally registers member pipelines.
func (s *APIServer) UpsertPipelineGroup(ctx context.Context, name, description, namePattern string, pipelines []string) (*PipelineGroup, error) {
	name = strings.TrimSpace(name)
	namePattern = strings.TrimSpace(namePattern)
	if name == "" {
		return nil, fmt.Errorf("pipeline group name is required")
	}
	if namePattern != "" {
		if _, err := path.Match(strings.ToLower(namePattern), "sample_pipeline"); err != nil {
			return nil, fmt.Errorf("invalid name_pattern %q: %w", namePattern, err)
		}
	}

	table := resolveTableName(s.dialect, TablePipelineGroups)
	var existingID int64
	checkQuery := fmt.Sprintf("SELECT id FROM %s WHERE LOWER(name) = LOWER(%s)", table, Placeholder(s.dialect, 1))
	err := s.db.QueryRowContext(ctx, checkQuery, name).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed querying %s: %w", table, err)
	}

	if err == nil && existingID > 0 {
		updQuery := fmt.Sprintf(
			"UPDATE %s SET description = %s, name_pattern = %s WHERE id = %s",
			table, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2), Placeholder(s.dialect, 3),
		)
		if _, err := s.db.ExecContext(ctx, updQuery, description, namePattern, existingID); err != nil {
			return nil, fmt.Errorf("failed updating pipeline group %q: %w", name, err)
		}
	} else {
		insQuery := BuildInsertQuery(s.dialect, TablePipelineGroups, []string{"name", "description", "name_pattern"})
		if _, err := s.db.ExecContext(ctx, insQuery, name, description, namePattern); err != nil {
			return nil, fmt.Errorf("failed creating pipeline group %q: %w", name, err)
		}
	}

	for _, p := range pipelines {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			if err := s.AddPipelineToGroup(ctx, name, trimmed); err != nil {
				return nil, err
			}
		}
	}

	return s.GetPipelineGroup(ctx, name)
}

// GetPipelineGroup retrieves a single pipeline group and its explicit member pipeline names.
func (s *APIServer) GetPipelineGroup(ctx context.Context, name string) (*PipelineGroup, error) {
	name = strings.TrimSpace(name)
	table := resolveTableName(s.dialect, TablePipelineGroups)
	query := fmt.Sprintf(
		"SELECT id, name, COALESCE(description, ''), COALESCE(name_pattern, '') FROM %s WHERE LOWER(name) = LOWER(%s)",
		table, Placeholder(s.dialect, 1),
	)

	var grp PipelineGroup
	if err := s.db.QueryRowContext(ctx, query, name).Scan(&grp.ID, &grp.Name, &grp.Description, &grp.NamePattern); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactNotFound
		}
		return nil, err
	}

	memTable := resolveTableName(s.dialect, TablePipelineGroupMembers)
	memQuery := fmt.Sprintf("SELECT pipeline_name FROM %s WHERE LOWER(group_name) = LOWER(%s) ORDER BY pipeline_name ASC", memTable, Placeholder(s.dialect, 1))
	rows, err := s.db.QueryContext(ctx, memQuery, grp.Name)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err == nil {
				grp.Pipelines = append(grp.Pipelines, p)
			}
		}
	}
	if grp.Pipelines == nil {
		grp.Pipelines = []string{}
	}
	return &grp, nil
}

// ListPipelineGroups lists all pipeline groups and their member pipelines.
func (s *APIServer) ListPipelineGroups(ctx context.Context) ([]PipelineGroup, error) {
	table := resolveTableName(s.dialect, TablePipelineGroups)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT id, name, COALESCE(description, ''), COALESCE(name_pattern, '') FROM %s ORDER BY name ASC", table))
	if err != nil {
		if isMissingTableError(err) {
			return []PipelineGroup{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	var groups []PipelineGroup
	idxByLower := make(map[string]int)
	for rows.Next() {
		var g PipelineGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.NamePattern); err != nil {
			return nil, err
		}
		g.Pipelines = []string{}
		idxByLower[strings.ToLower(g.Name)] = len(groups)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if groups == nil {
		return []PipelineGroup{}, nil
	}

	memTable := resolveTableName(s.dialect, TablePipelineGroupMembers)
	mRows, mErr := s.db.QueryContext(ctx, fmt.Sprintf("SELECT group_name, pipeline_name FROM %s ORDER BY pipeline_name ASC", memTable))
	if mErr == nil {
		defer mRows.Close()
		for mRows.Next() {
			var gName, pName string
			if err := mRows.Scan(&gName, &pName); err == nil {
				if idx, ok := idxByLower[strings.ToLower(strings.TrimSpace(gName))]; ok {
					groups[idx].Pipelines = append(groups[idx].Pipelines, pName)
				}
			}
		}
	}
	return groups, nil
}

// DeletePipelineGroup deletes a pipeline group and cleans up its memberships and ACL rules.
func (s *APIServer) DeletePipelineGroup(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	table := resolveTableName(s.dialect, TablePipelineGroups)
	res, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(name) = LOWER(%s)", table, Placeholder(s.dialect, 1)), name)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrArtifactNotFound
	}

	memTable := resolveTableName(s.dialect, TablePipelineGroupMembers)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(group_name) = LOWER(%s)", memTable, Placeholder(s.dialect, 1)), name)

	aclTable := resolveTableName(s.dialect, TablePipelineACL)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(target_type) = 'pipeline_group' AND LOWER(target_name) = LOWER(%s)", aclTable, Placeholder(s.dialect, 1)), name)
	return nil
}

// AddPipelineToGroup assigns a pipeline name to an existing pipeline group (idempotent).
func (s *APIServer) AddPipelineToGroup(ctx context.Context, groupName, pipelineName string) error {
	groupName = strings.TrimSpace(groupName)
	pipelineName = strings.TrimSpace(pipelineName)
	if groupName == "" || pipelineName == "" {
		return fmt.Errorf("group_name and pipeline_name are required")
	}

	grp, err := s.GetPipelineGroup(ctx, groupName)
	if err != nil {
		return fmt.Errorf("pipeline group %q not found: %w", groupName, err)
	}

	memTable := resolveTableName(s.dialect, TablePipelineGroupMembers)
	checkQuery := fmt.Sprintf(
		"SELECT pipeline_name FROM %s WHERE LOWER(group_name) = LOWER(%s) AND LOWER(pipeline_name) = LOWER(%s)",
		memTable, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2),
	)
	var existing string
	if err := s.db.QueryRowContext(ctx, checkQuery, grp.Name, pipelineName).Scan(&existing); err == nil {
		return nil // already assigned
	}

	insQuery := BuildInsertQuery(s.dialect, TablePipelineGroupMembers, []string{"group_name", "pipeline_name"})
	_, err = s.db.ExecContext(ctx, insQuery, grp.Name, pipelineName)
	return err
}

// RemovePipelineFromGroup unassigns a pipeline from a pipeline group.
func (s *APIServer) RemovePipelineFromGroup(ctx context.Context, groupName, pipelineName string) error {
	groupName = strings.TrimSpace(groupName)
	pipelineName = strings.TrimSpace(pipelineName)
	memTable := resolveTableName(s.dialect, TablePipelineGroupMembers)
	query := fmt.Sprintf(
		"DELETE FROM %s WHERE LOWER(group_name) = LOWER(%s) AND LOWER(pipeline_name) = LOWER(%s)",
		memTable, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2),
	)
	res, err := s.db.ExecContext(ctx, query, groupName, pipelineName)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrArtifactNotFound
	}
	return nil
}

// CleanupPipelineReferences removes group memberships and direct pipeline ACL entries when a pipeline is deleted.
func (s *APIServer) CleanupPipelineReferences(ctx context.Context, pipelineName string) {
	if s == nil || s.db == nil || strings.TrimSpace(pipelineName) == "" {
		return
	}
	memTable := resolveTableName(s.dialect, TablePipelineGroupMembers)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(pipeline_name) = LOWER(%s)", memTable, Placeholder(s.dialect, 1)), pipelineName)

	aclTable := resolveTableName(s.dialect, TablePipelineACL)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(target_type) = 'pipeline' AND LOWER(target_name) = LOWER(%s)", aclTable, Placeholder(s.dialect, 1)), pipelineName)
}

// CleanupUserReferences removes user group memberships and direct user ACL entries when a user is deleted.
func (s *APIServer) CleanupUserReferences(ctx context.Context, username string) {
	if s == nil || s.db == nil || strings.TrimSpace(username) == "" {
		return
	}
	memTable := resolveTableName(s.dialect, TableAPIUserGroups)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(username) = LOWER(%s)", memTable, Placeholder(s.dialect, 1)), username)

	aclTable := resolveTableName(s.dialect, TablePipelineACL)
	_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE LOWER(principal_type) = 'user' AND LOWER(principal_name) = LOWER(%s)", aclTable, Placeholder(s.dialect, 1)), username)
}

// =============================================================================
// 3. Pipeline ACL CRUD Operations (TablePipelineACL)
// =============================================================================

func validateAndNormalizeACL(rule PipelineACL) (PipelineACL, error) {
	pType, err := normalizePrincipalType(rule.PrincipalType)
	if err != nil {
		return rule, err
	}
	pName := strings.TrimSpace(rule.PrincipalName)
	if pName == "" {
		return rule, fmt.Errorf("principal_name is required")
	}

	tType, err := normalizeTargetType(rule.TargetType)
	if err != nil {
		return rule, err
	}
	tName := strings.TrimSpace(rule.TargetName)
	if tType == TargetWildcard {
		if tName == "" {
			tName = TargetWildcard
		} else if tName != TargetWildcard {
			return rule, fmt.Errorf("target_name must be '*' when target_type is '*'")
		}
	} else if tName == "" {
		return rule, fmt.Errorf("target_name is required when target_type is %q", tType)
	}

	normPerms, _, err := NormalizeACLActions(rule.Permissions)
	if err != nil {
		return rule, err
	}

	eff, err := normalizeEffect(rule.Effect)
	if err != nil {
		return rule, err
	}

	rule.PrincipalType = pType
	rule.PrincipalName = pName
	rule.TargetType = tType
	rule.TargetName = tName
	rule.Permissions = normPerms
	rule.Effect = eff
	rule.Description = strings.TrimSpace(rule.Description)
	return rule, nil
}

// UpsertPipelineACL creates or updates a granular ACL rule matching (principal_type, principal_name, target_type, target_name, effect).
func (s *APIServer) UpsertPipelineACL(ctx context.Context, rule PipelineACL) (*PipelineACL, error) {
	norm, err := validateAndNormalizeACL(rule)
	if err != nil {
		return nil, err
	}

	table := resolveTableName(s.dialect, TablePipelineACL)
	findQuery := fmt.Sprintf(
		"SELECT id FROM %s WHERE LOWER(principal_type) = LOWER(%s) AND LOWER(principal_name) = LOWER(%s) AND LOWER(target_type) = LOWER(%s) AND LOWER(target_name) = LOWER(%s) AND LOWER(effect) = LOWER(%s)",
		table,
		Placeholder(s.dialect, 1),
		Placeholder(s.dialect, 2),
		Placeholder(s.dialect, 3),
		Placeholder(s.dialect, 4),
		Placeholder(s.dialect, 5),
	)

	var existingID int64
	err = s.db.QueryRowContext(ctx, findQuery, norm.PrincipalType, norm.PrincipalName, norm.TargetType, norm.TargetName, norm.Effect).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed querying %s: %w", table, err)
	}

	if err == nil && existingID > 0 {
		updQuery := fmt.Sprintf(
			"UPDATE %s SET permissions = %s, description = %s WHERE id = %s",
			table, Placeholder(s.dialect, 1), Placeholder(s.dialect, 2), Placeholder(s.dialect, 3),
		)
		if _, err := s.db.ExecContext(ctx, updQuery, norm.Permissions, norm.Description, existingID); err != nil {
			return nil, fmt.Errorf("failed updating ACL #%d in %s: %w", existingID, table, err)
		}
		norm.ID = existingID
		return &norm, nil
	}

	insQuery := BuildInsertQuery(s.dialect, TablePipelineACL, []string{
		"principal_type", "principal_name", "target_type", "target_name", "permissions", "effect", "description",
	})
	if _, err := s.db.ExecContext(ctx, insQuery, norm.PrincipalType, norm.PrincipalName, norm.TargetType, norm.TargetName, norm.Permissions, norm.Effect, norm.Description); err != nil {
		return nil, fmt.Errorf("failed inserting ACL into %s: %w", table, err)
	}

	_ = s.db.QueryRowContext(ctx, findQuery, norm.PrincipalType, norm.PrincipalName, norm.TargetType, norm.TargetName, norm.Effect).Scan(&norm.ID)
	return &norm, nil
}

// UpdatePipelineACLByID updates an existing ACL rule by primary key ID.
func (s *APIServer) UpdatePipelineACLByID(ctx context.Context, id int64, rule PipelineACL) (*PipelineACL, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid ACL ID %d", id)
	}
	norm, err := validateAndNormalizeACL(rule)
	if err != nil {
		return nil, err
	}

	table := resolveTableName(s.dialect, TablePipelineACL)
	updQuery := fmt.Sprintf(
		"UPDATE %s SET principal_type = %s, principal_name = %s, target_type = %s, target_name = %s, permissions = %s, effect = %s, description = %s WHERE id = %s",
		table,
		Placeholder(s.dialect, 1),
		Placeholder(s.dialect, 2),
		Placeholder(s.dialect, 3),
		Placeholder(s.dialect, 4),
		Placeholder(s.dialect, 5),
		Placeholder(s.dialect, 6),
		Placeholder(s.dialect, 7),
		Placeholder(s.dialect, 8),
	)
	res, err := s.db.ExecContext(ctx, updQuery, norm.PrincipalType, norm.PrincipalName, norm.TargetType, norm.TargetName, norm.Permissions, norm.Effect, norm.Description, id)
	if err != nil {
		return nil, err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return nil, ErrArtifactNotFound
	}
	norm.ID = id
	return &norm, nil
}

// DeletePipelineACLByID deletes an ACL rule by primary key ID.
func (s *APIServer) DeletePipelineACLByID(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("invalid ACL ID %d", id)
	}
	table := resolveTableName(s.dialect, TablePipelineACL)
	res, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = %s", table, Placeholder(s.dialect, 1)), id)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrArtifactNotFound
	}
	return nil
}

// ListPipelineACLs returns ACL rules filtered by optional principal/target criteria.
func (s *APIServer) ListPipelineACLs(ctx context.Context, principalType, principalName, targetType, targetName string) ([]PipelineACL, error) {
	table := resolveTableName(s.dialect, TablePipelineACL)

	var clauses []string
	var args []any
	argIdx := 1

	if strings.TrimSpace(principalType) != "" {
		clauses = append(clauses, fmt.Sprintf("LOWER(principal_type) = LOWER(%s)", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(principalType))
		argIdx++
	}
	if strings.TrimSpace(principalName) != "" {
		clauses = append(clauses, fmt.Sprintf("LOWER(principal_name) = LOWER(%s)", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(principalName))
		argIdx++
	}
	if strings.TrimSpace(targetType) != "" {
		clauses = append(clauses, fmt.Sprintf("LOWER(target_type) = LOWER(%s)", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(targetType))
		argIdx++
	}
	if strings.TrimSpace(targetName) != "" {
		clauses = append(clauses, fmt.Sprintf("LOWER(target_name) = LOWER(%s)", Placeholder(s.dialect, argIdx)))
		args = append(args, strings.TrimSpace(targetName))
		argIdx++
	}

	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(
		"SELECT id, principal_type, principal_name, target_type, target_name, permissions, COALESCE(effect, 'allow'), COALESCE(description, '') FROM %s%s ORDER BY id ASC",
		table, whereSQL,
	)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingTableError(err) {
			return []PipelineACL{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	var rules []PipelineACL
	for rows.Next() {
		var r PipelineACL
		if err := rows.Scan(&r.ID, &r.PrincipalType, &r.PrincipalName, &r.TargetType, &r.TargetName, &r.Permissions, &r.Effect, &r.Description); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	if rules == nil {
		rules = []PipelineACL{}
	}
	return rules, rows.Err()
}

// =============================================================================
// 4. ACL Evaluation Engine (Batch & Single-Resource)
// =============================================================================

type aclSnapshot struct {
	pipelineGroups        []PipelineGroup
	groupsByPipelineLower map[string][]string // lower(pipeline_name) -> canonical group names
	acls                  []PipelineACL
}

func (s *APIServer) loadACLSnapshot(ctx context.Context) (*aclSnapshot, error) {
	snap := &aclSnapshot{
		groupsByPipelineLower: make(map[string][]string),
	}
	if s == nil || s.db == nil {
		return snap, nil
	}

	pGroups, err := s.ListPipelineGroups(ctx)
	if err != nil {
		return nil, err
	}
	snap.pipelineGroups = pGroups
	for _, pg := range pGroups {
		for _, pName := range pg.Pipelines {
			key := strings.ToLower(strings.TrimSpace(pName))
			if key != "" {
				snap.groupsByPipelineLower[key] = append(snap.groupsByPipelineLower[key], pg.Name)
			}
		}
	}

	acls, err := s.ListPipelineACLs(ctx, "", "", "", "")
	if err != nil {
		return nil, err
	}
	snap.acls = acls
	return snap, nil
}

func (snap *aclSnapshot) groupsForPipeline(pipelineName string, extraGroups []string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(g string) {
		g = strings.TrimSpace(g)
		if g == "" {
			return
		}
		k := strings.ToLower(g)
		if _, exists := seen[k]; !exists {
			seen[k] = struct{}{}
			out = append(out, g)
		}
	}

	if pipelineName != "" {
		for _, g := range snap.groupsByPipelineLower[strings.ToLower(strings.TrimSpace(pipelineName))] {
			add(g)
		}
		for _, pg := range snap.pipelineGroups {
			if pg.NamePattern != "" && matchPipelinePattern(pg.NamePattern, pipelineName) {
				add(pg.Name)
			}
		}
	}
	for _, eg := range extraGroups {
		add(eg)
	}
	sort.Strings(out)
	return out
}

func principalSetsForUser(user *APIUser) (userSet map[string]struct{}, groupSet map[string]struct{}) {
	userSet = make(map[string]struct{}, 4)
	groupSet = make(map[string]struct{}, len(user.Groups)*2+4)

	uname := strings.TrimSpace(user.Username)
	userDomain := ""
	if uname != "" {
		userSet[strings.ToLower(uname)] = struct{}{}
		if idx := strings.LastIndex(uname, `\`); idx >= 0 && idx+1 < len(uname) {
			userDomain = uname[:idx]
			userSet[strings.ToLower(uname[idx+1:])] = struct{}{}
		}
	}

	for _, g := range user.Groups {
		trimmed := strings.TrimSpace(g)
		if trimmed == "" {
			continue
		}
		groupSet[strings.ToLower(trimmed)] = struct{}{}
		if slashIdx := strings.LastIndex(trimmed, `\`); slashIdx >= 0 && slashIdx+1 < len(trimmed) {
			groupSet[strings.ToLower(trimmed[slashIdx+1:])] = struct{}{}
		} else if userDomain != "" {
			groupSet[strings.ToLower(userDomain+`\`+trimmed)] = struct{}{}
		}
	}
	return userSet, groupSet
}

func legacyGlobalAllowsAction(user *APIUser, action string) bool {
	if user == nil || !user.IsActive {
		return false
	}
	switch action {
	case ActionRead:
		return user.HasPermission(PermReadOnly)
	case ActionCreate, ActionUpdate, ActionDelete:
		return user.HasPermission(PermWrite)
	case ActionExecute:
		return user.HasPermission(PermExecutionOnly)
	default:
		return false
	}
}

func (snap *aclSnapshot) evaluate(user *APIUser, pipelineName string, candidateGroups []string, action string, strictACL bool) (bool, string) {
	if user == nil || !user.IsActive {
		return false, "user is unauthenticated or disabled"
	}
	if user.HasPermission(PermAll) {
		return true, "allowed by global 'all' permission"
	}

	userSet, groupSet := principalSetsForUser(user)
	resolvedGroups := snap.groupsForPipeline(pipelineName, candidateGroups)
	pipelineGroupSet := make(map[string]struct{}, len(resolvedGroups))
	for _, g := range resolvedGroups {
		pipelineGroupSet[strings.ToLower(g)] = struct{}{}
	}

	var matchedAllows []PipelineACL
	for _, rule := range snap.acls {
		pType := strings.ToLower(strings.TrimSpace(rule.PrincipalType))
		pNameLower := strings.ToLower(strings.TrimSpace(rule.PrincipalName))

		principalMatches := false
		if pType == PrincipalUser {
			_, principalMatches = userSet[pNameLower]
		} else if pType == PrincipalGroup {
			_, principalMatches = groupSet[pNameLower]
		}
		if !principalMatches {
			continue
		}

		if !aclGrantsAction(rule.Permissions, action) {
			continue
		}

		tType := strings.ToLower(strings.TrimSpace(rule.TargetType))
		tNameLower := strings.ToLower(strings.TrimSpace(rule.TargetName))
		targetMatches := false
		switch tType {
		case TargetWildcard:
			targetMatches = true
		case TargetPipeline:
			targetMatches = pipelineName != "" && strings.EqualFold(strings.TrimSpace(rule.TargetName), strings.TrimSpace(pipelineName))
		case TargetPipelineGroup:
			_, targetMatches = pipelineGroupSet[tNameLower]
		}
		if !targetMatches {
			continue
		}

		if strings.EqualFold(rule.Effect, EffectDeny) {
			return false, fmt.Sprintf("explicitly denied by ACL #%d (%s:%s -> %s:%s)", rule.ID, rule.PrincipalType, rule.PrincipalName, rule.TargetType, rule.TargetName)
		}
		matchedAllows = append(matchedAllows, rule)
	}

	// Special verification when creating or assigning explicit candidate pipeline groups:
	// If candidateGroups are requested and user is not allowed globally ('*') or directly on the pipeline name
	// (or via legacy global write), every requested candidate group must be covered by an allow ACL.
	if len(candidateGroups) > 0 && (action == ActionCreate || action == ActionUpdate) {
		hasDirectOrWildcard := false
		allowedGroupSet := make(map[string]struct{})
		for _, rule := range matchedAllows {
			tt := strings.ToLower(strings.TrimSpace(rule.TargetType))
			if tt == TargetWildcard || tt == TargetPipeline {
				hasDirectOrWildcard = true
			} else if tt == TargetPipelineGroup {
				allowedGroupSet[strings.ToLower(strings.TrimSpace(rule.TargetName))] = struct{}{}
			}
		}
		if hasDirectOrWildcard {
			return true, fmt.Sprintf("allowed by ACL #%d", matchedAllows[0].ID)
		}
		if !strictACL && legacyGlobalAllowsAction(user, action) {
			return true, "allowed by global user permission"
		}
		for _, cg := range candidateGroups {
			cgTrim := strings.TrimSpace(cg)
			if cgTrim == "" {
				continue
			}
			if _, ok := allowedGroupSet[strings.ToLower(cgTrim)]; !ok {
				return false, fmt.Sprintf("user %q lacks %q permission on pipeline group %q", user.Username, action, cgTrim)
			}
		}
		if len(matchedAllows) > 0 {
			return true, fmt.Sprintf("allowed by pipeline group ACL #%d", matchedAllows[0].ID)
		}
	}

	if len(matchedAllows) > 0 {
		r := matchedAllows[0]
		return true, fmt.Sprintf("allowed by ACL #%d (%s:%s -> %s:%s)", r.ID, r.PrincipalType, r.PrincipalName, r.TargetType, r.TargetName)
	}

	if !strictACL && legacyGlobalAllowsAction(user, action) {
		return true, "allowed by global user permission"
	}

	if pipelineName != "" {
		return false, fmt.Sprintf("user %q lacks %q permission on pipeline %q", user.Username, action, pipelineName)
	}
	return false, fmt.Sprintf("user %q lacks %q permission", user.Username, action)
}

func (s *APIServer) isStrictACL() bool {
	return s != nil && s.cfg != nil && s.cfg.StrictACL
}

// EvaluatePipelineAccess checks whether user is authorized to perform action on pipelineName (and optional candidateGroups).
func (s *APIServer) EvaluatePipelineAccess(ctx context.Context, user *APIUser, pipelineName string, candidateGroups []string, action string) (bool, string, error) {
	if user == nil || !user.IsActive {
		return false, "authentication required", nil
	}
	if user.HasPermission(PermAll) {
		return true, "allowed by global 'all' permission", nil
	}
	snap, err := s.loadACLSnapshot(ctx)
	if err != nil {
		return false, "", err
	}
	allowed, reason := snap.evaluate(user, pipelineName, candidateGroups, action, s.isStrictACL())
	return allowed, reason, nil
}

// FilterAccessiblePipelines filters a slice of pipeline ArtifactRecords to those readable by user,
// and enriches each returned record with its resolved pipeline groups.
func (s *APIServer) FilterAccessiblePipelines(ctx context.Context, user *APIUser, items []ArtifactRecord, action string) ([]ArtifactRecord, error) {
	snap, err := s.loadACLSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	strict := s.isStrictACL()
	out := make([]ArtifactRecord, 0, len(items))
	for _, rec := range items {
		if rec.Type != "pipeline" {
			if user != nil && (user.HasPermission(PermAll) || (!strict && legacyGlobalAllowsAction(user, action))) {
				out = append(out, rec)
			}
			continue
		}
		allowed, _ := snap.evaluate(user, rec.Name, nil, action, strict)
		if allowed {
			rec.Groups = snap.groupsForPipeline(rec.Name, nil)
			out = append(out, rec)
		}
	}
	return out, nil
}

// CanUserExecuteInlineOrExternal checks whether user is allowed to execute raw inline XML,
// local filesystem paths, or external sql:// URIs that are not registered in TablePipelineContent.
func (s *APIServer) CanUserExecuteInlineOrExternal(user *APIUser) bool {
	if user == nil || !user.IsActive {
		return false
	}
	if user.HasPermission(PermAll) {
		return true
	}
	if s.isStrictACL() {
		return false
	}
	// Legacy global execution-only / write roles retain inline/local parity; 'scoped' users cannot bypass pipeline ACLs.
	return user.HasPermission(PermExecutionOnly)
}

// ComputeEffectivePermissions calculates effective CRUD + execute permissions across all stored pipelines for a target user.
func (s *APIServer) ComputeEffectivePermissions(ctx context.Context, targetUser *APIUser) ([]EffectivePipelinePermission, error) {
	pipelines, err := s.ListArtifacts(ctx, "pipeline", false)
	if err != nil {
		return nil, err
	}
	snap, err := s.loadACLSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	strict := s.isStrictACL()

	result := make([]EffectivePipelinePermission, 0, len(pipelines))
	for _, p := range pipelines {
		var allowed, denied []string
		for _, act := range canonicalActionOrder {
			ok, reason := snap.evaluate(targetUser, p.Name, nil, act, strict)
			if ok {
				allowed = append(allowed, act)
			} else if strings.HasPrefix(reason, "explicitly denied") {
				denied = append(denied, act)
			}
		}
		if len(allowed) > 0 || len(denied) > 0 || targetUser.HasPermission(PermAll) {
			if allowed == nil {
				allowed = []string{}
			}
			result = append(result, EffectivePipelinePermission{
				PipelineID:     p.ID,
				PipelineName:   p.Name,
				Groups:         snap.groupsForPipeline(p.Name, nil),
				AllowedActions: allowed,
				DeniedActions:  denied,
			})
		}
	}
	return result, nil
}

// ExtractPipelineNameFromRunFilePath extracts the canonical pipeline name from pipeline_runs.file_path
// (e.g. "db://pipeline/daily_etl" or "db://pipeline/id/1(daily_etl)"). Returns "" for inline or external file runs.
func ExtractPipelineNameFromRunFilePath(filePath string) string {
	fp := strings.TrimSpace(filePath)
	if fp == "" {
		return ""
	}
	if strings.HasPrefix(fp, "db://pipeline/id/") {
		if openIdx := strings.IndexByte(fp, '('); openIdx >= 0 && strings.HasSuffix(fp, ")") {
			return strings.TrimSpace(fp[openIdx+1 : len(fp)-1])
		}
		return ""
	}
	if strings.HasPrefix(fp, "db://pipeline/") {
		return strings.TrimSpace(strings.TrimPrefix(fp, "db://pipeline/"))
	}
	return ""
}
