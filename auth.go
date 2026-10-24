package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Permission Scope Constants
const (
	PermExecutionOnly = "execution-only"
	PermReadOnly      = "read-only"
	PermWrite         = "write"
	PermUpload        = "upload"
	PermAll           = "all"

	WindowsIntegratedAuthMarker = "__WINDOWS_INTEGRATED__"
	WindowsAutoHostUserMarker   = "__AUTO_HOST__"
	HeaderWindowsUser           = "X-Flow-Windows-User"
	HeaderWindowsGroups         = "X-Flow-Windows-Groups"
)

// APIUser represents a row in TableAPIUsers (dbo.flow_api_users).
type APIUser struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Permission   string    `json:"permission"`
	IsActive     bool      `json:"is_active"`
	Description  string    `json:"description,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type contextKey string

const authUserContextKey contextKey = "auth_user"

// UserFromContext retrieves the authenticated *APIUser from the request context.
func UserFromContext(ctx context.Context) *APIUser {
	if u, ok := ctx.Value(authUserContextKey).(*APIUser); ok {
		return u
	}
	return nil
}

// HasPermission checks if the user's permission string grants the required capability.
// Supported role values in TableAPIUsers.permission:
//   - "execution-only" (or "execution_only", "execute"): can execute/validate pipelines and read/list/export artifacts & logs
//   - "read-only"      (or "read_only", "read"):        can read/list/export artifacts and query logs
//   - "write" / "upload" (or "write-only", "read-write"): can upload/create/update/delete/read artifacts
//   - "all"            (or "admin", "*"):               full access to all endpoints
// Multiple permissions may also be combined with commas (e.g. "read-only,execution-only").
func (u *APIUser) HasPermission(required string) bool {
	if u == nil || !u.IsActive {
		return false
	}

	reqNorm := normalizePermToken(required)
	tokens := strings.Split(u.Permission, ",")
	for _, raw := range tokens {
		tok := normalizePermToken(raw)
		if tok == PermAll {
			return true
		}
		switch reqNorm {
		case PermReadOnly:
			// "read-only", "execution-only", and "write"/"upload" can read artifacts and logs
			if tok == PermReadOnly || tok == PermExecutionOnly || tok == PermWrite || tok == PermUpload {
				return true
			}
		case PermWrite, PermUpload:
			if tok == PermWrite || tok == PermUpload {
				return true
			}
		case PermExecutionOnly:
			// "execution-only" and "write" can execute pipelines
			if tok == PermExecutionOnly || tok == PermWrite {
				return true
			}
		case PermAll:
			if tok == PermAll {
				return true
			}
		}
	}
	return false
}

func normalizePermToken(p string) string {
	s := strings.ToLower(strings.TrimSpace(p))
	switch s {
	case "all", "admin", "*", "full":
		return PermAll
	case "execution-only", "execution_only", "execute", "exec", "runner":
		return PermExecutionOnly
	case "read-only", "read_only", "read", "readonly", "viewer":
		return PermReadOnly
	case "upload", "upload-only", "upload_only":
		return PermUpload
	case "write", "write-only", "write_only", "read-write", "editor":
		return PermWrite
	default:
		return s
	}
}

func permRank(p string) int {
	best := 0
	for _, part := range strings.Split(p, ",") {
		switch normalizePermToken(part) {
		case PermAll:
			if 5 > best {
				best = 5
			}
		case PermWrite:
			if 4 > best {
				best = 4
			}
		case PermUpload:
			if 3 > best {
				best = 3
			}
		case PermExecutionOnly:
			if 2 > best {
				best = 2
			}
		case PermReadOnly:
			if 1 > best {
				best = 1
			}
		}
	}
	return best
}

func rankToPerm(rank int) string {
	switch rank {
	case 5:
		return PermAll
	case 4:
		return PermWrite
	case 3:
		return PermUpload
	case 2:
		return PermExecutionOnly
	default:
		return PermReadOnly
	}
}

// ValidatePermissionString checks that a permission string contains only valid permission tokens.
func ValidatePermissionString(perm string) error {
	if strings.TrimSpace(perm) == "" {
		return fmt.Errorf("permission cannot be empty; expected execution-only, read-only, write, upload, or all")
	}
	for _, part := range strings.Split(perm, ",") {
		norm := normalizePermToken(part)
		switch norm {
		case PermAll, PermExecutionOnly, PermReadOnly, PermWrite, PermUpload:
			// valid
		default:
			return fmt.Errorf("unsupported permission %q: expected execution-only, read-only, write, upload, or all", strings.TrimSpace(part))
		}
	}
	return nil
}

// VerifyPassword compares a candidate password against storedHash.
// Supports:
//  1. Bcrypt hashes ($2a$, $2b$, $2y$)
//  2. SHA-256 hex hashes prefixed with "sha256:"
//  3. Constant-time plaintext comparison (using SHA-256 digests so length is not leaked)
func VerifyPassword(storedHash, candidate string) bool {
	stored := strings.TrimSpace(storedHash)
	if stored == "" || strings.EqualFold(stored, "WINDOWS") || strings.EqualFold(stored, WindowsIntegratedAuthMarker) {
		return false
	}

	if strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") || strings.HasPrefix(stored, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(candidate)) == nil
	}

	if strings.HasPrefix(strings.ToLower(stored), "sha256:") {
		expectedHex := strings.TrimSpace(stored[len("sha256:"):])
		sum := sha256.Sum256([]byte(candidate))
		candidateHex := hex.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(strings.ToLower(expectedHex)), []byte(candidateHex)) == 1
	}

	storedDigest := sha256.Sum256([]byte(stored))
	candDigest := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(storedDigest[:], candDigest[:]) == 1
}

// HashPasswordBcrypt hashes a plaintext password using bcrypt for storage in TableAPIUsers.
func HashPasswordBcrypt(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// AuthenticateUser looks up username in TableAPIUsers and verifies the password.
func (s *APIServer) AuthenticateUser(ctx context.Context, username, password string) (*APIUser, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	table := resolveTableName(s.dialect, TableAPIUsers)
	ph := Placeholder(s.dialect, 1)

	query := fmt.Sprintf(
		"SELECT username, password_hash, permission, is_active, COALESCE(description, '') FROM %s WHERE username = %s",
		table, ph,
	)

	var user APIUser
	var activeRaw any
	err := s.db.QueryRowContext(ctx, query, username).Scan(
		&user.Username,
		&user.PasswordHash,
		&user.Permission,
		&activeRaw,
		&user.Description,
	)
	if err != nil {
		return nil, err
	}

	user.IsActive = parseDBBool(activeRaw)
	if !user.IsActive {
		return nil, fmt.Errorf("user %q is disabled", username)
	}

	if !VerifyPassword(user.PasswordHash, password) {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &user, nil
}

func isContainerOrAutoUser(u string) bool {
	u = strings.TrimSpace(strings.ToLower(u))
	if u == "" || u == strings.ToLower(WindowsAutoHostUserMarker) || u == "app" || u == "root" || u == "flow" || u == "nonroot" {
		return true
	}
	for _, suffix := range []string{`\app`, `\root`, `\flow`, `/app`, `/root`, `/flow`} {
		if strings.HasSuffix(u, suffix) {
			return true
		}
	}
	return false
}

// resolveWindowsUserAndGroups resolves the active host Windows identity when windowsUser is "__AUTO_HOST__"
// or a Linux container service account (e8be6e800f93\app), and enriches windowsGroups with the user's
// Windows / Active Directory group memberships from the host OS.
func resolveWindowsUserAndGroups(windowsUser string, windowsGroups []string) (string, []string) {
	windowsUser = strings.TrimSpace(windowsUser)
	var targetOSUser *user.User

	if isContainerOrAutoUser(windowsUser) {
		if envWin := strings.TrimSpace(os.Getenv("FLOW_WINDOWS_USER")); envWin != "" {
			windowsUser = envWin
		} else if hostUser := strings.TrimSpace(os.Getenv("HOST_USERNAME")); hostUser != "" {
			hostDom := strings.TrimSpace(os.Getenv("HOST_USERDOMAIN"))
			if hostDom != "" {
				windowsUser = hostDom + `\` + hostUser
			} else {
				windowsUser = hostUser
			}
		} else if curr, err := user.Current(); err == nil && curr != nil && !isContainerOrAutoUser(curr.Username) {
			windowsUser = strings.TrimSpace(curr.Username)
			targetOSUser = curr
		} else {
			dom := strings.TrimSpace(os.Getenv("USERDOMAIN"))
			usr := strings.TrimSpace(os.Getenv("USERNAME"))
			if usr != "" && !isContainerOrAutoUser(usr) {
				if dom != "" {
					windowsUser = dom + `\` + usr
				} else {
					windowsUser = usr
				}
			}
		}
	}

	if targetOSUser == nil && windowsUser != "" {
		if curr, err := user.Current(); err == nil && curr != nil && strings.EqualFold(strings.TrimSpace(curr.Username), windowsUser) {
			targetOSUser = curr
		} else if lookedUp, err := user.Lookup(windowsUser); err == nil && lookedUp != nil {
			targetOSUser = lookedUp
		}
	}

	mergedGroups := make([]string, 0, len(windowsGroups)+16)
	seen := make(map[string]struct{}, len(windowsGroups)+16)
	addGroup := func(g string) {
		g = strings.TrimSpace(g)
		if g == "" {
			return
		}
		key := strings.ToLower(g)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			mergedGroups = append(mergedGroups, g)
		}
	}

	for _, g := range windowsGroups {
		addGroup(g)
	}

	if targetOSUser != nil {
		if gids, err := targetOSUser.GroupIds(); err == nil {
			for _, gid := range gids {
				for _, qName := range lookupGroupSidQualified(gid) {
					addGroup(qName)
				}
				if grp, err := user.LookupGroupId(gid); err == nil && grp != nil {
					addGroup(grp.Name)
				}
			}
		}
	}

	return windowsUser, mergedGroups
}

// AuthenticateWindowsIdentity matches a Windows user (DOMAIN\user or user) or any of their
// Windows/AD groups (DOMAIN\group) against active entries in TableAPIUsers.
func (s *APIServer) AuthenticateWindowsIdentity(ctx context.Context, windowsUser string, windowsGroups []string) (*APIUser, error) {
	return s.AuthenticateWindowsIdentityWithPassword(ctx, windowsUser, "", windowsGroups)
}

// AuthenticateWindowsIdentityWithPassword matches a Windows user (DOMAIN\user or user) or any of their
// Windows/AD groups (DOMAIN\group) against active entries in TableAPIUsers, optionally verifying a password
// if the user row has a stored password hash.
func (s *APIServer) AuthenticateWindowsIdentityWithPassword(ctx context.Context, windowsUser, password string, windowsGroups []string) (*APIUser, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	windowsUser, windowsGroups = resolveWindowsUserAndGroups(windowsUser, windowsGroups)
	if windowsUser == "" {
		return nil, fmt.Errorf("windows username is empty")
	}

	userDomain := ""
	shortUser := windowsUser
	if idx := strings.LastIndex(windowsUser, `\`); idx >= 0 && idx+1 < len(windowsUser) {
		userDomain = windowsUser[:idx]
		shortUser = windowsUser[idx+1:]
	}

	users, err := s.ListAPIUsers(ctx)
	if err != nil {
		return nil, err
	}

	groupSet := make(map[string]struct{}, len(windowsGroups)*2)
	for _, g := range windowsGroups {
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

	bestRank := 0
	var matchedPrincipals []string

	for _, row := range users {
		rowName := strings.TrimSpace(row.Username)
		if rowName == "" {
			continue
		}

		isDirectUser := strings.EqualFold(rowName, windowsUser) || strings.EqualFold(rowName, shortUser)
		_, isGroupMatch := groupSet[strings.ToLower(rowName)]

		if !isDirectUser && !isGroupMatch {
			continue
		}

		if isDirectUser && !row.IsActive {
			return nil, fmt.Errorf("windows user %q is disabled in %s", windowsUser, TableAPIUsers)
		}
		if !row.IsActive {
			continue
		}

		// Only enforce DB password hash when the row is a non-domain local API user and not a DOMAIN\user Windows principal
		if isDirectUser && password != "" && password != WindowsIntegratedAuthMarker && !strings.Contains(rowName, `\`) && !strings.Contains(windowsUser, `\`) {
			stored := strings.TrimSpace(row.PasswordHash)
			if stored != "" && !strings.EqualFold(stored, "WINDOWS") && !strings.EqualFold(stored, WindowsIntegratedAuthMarker) {
				if !VerifyPassword(stored, password) {
					return nil, fmt.Errorf("invalid credentials for %q", windowsUser)
				}
			}
		}

		matchedPrincipals = append(matchedPrincipals, rowName)
		if r := permRank(row.Permission); r > bestRank {
			bestRank = r
		}
	}

	if bestRank == 0 || len(matchedPrincipals) == 0 {
		return nil, fmt.Errorf("windows user %q (and their AD/Windows groups) is not registered in %s", windowsUser, TableAPIUsers)
	}

	resolvedPerm := rankToPerm(bestRank)
	desc := fmt.Sprintf("Windows Identity (%s via %s)", windowsUser, strings.Join(matchedPrincipals, ", "))

	return &APIUser{
		Username:    windowsUser,
		Permission:  resolvedPerm,
		IsActive:    true,
		Description: desc,
	}, nil
}

func parseWindowsGroupsHeader(rawHeader string) []string {
	rawHeader = strings.TrimSpace(rawHeader)
	if rawHeader == "" {
		return nil
	}

	// Support base64-encoded JSON array or newline/comma-separated list
	if decoded, err := base64.StdEncoding.DecodeString(rawHeader); err == nil && len(decoded) > 0 {
		var jsonGroups []string
		if err := json.Unmarshal(decoded, &jsonGroups); err == nil {
			return jsonGroups
		}
		rawHeader = string(decoded)
	}

	parts := strings.FieldsFunc(rawHeader, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	var out []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// AuthenticateRequest inspects the HTTP request for either Windows Integrated identity/group headers
// or standard HTTP Basic Auth credentials.
func (s *APIServer) AuthenticateRequest(r *http.Request) (*APIUser, string, bool, error) {
	winUserHeader := strings.TrimSpace(r.Header.Get(HeaderWindowsUser))
	basicUser, basicPass, hasBasic := r.BasicAuth()

	if winUserHeader != "" || (hasBasic && basicPass == WindowsIntegratedAuthMarker) {
		winUser := winUserHeader
		if winUser == "" {
			winUser = strings.TrimSpace(basicUser)
		}
		groups := parseWindowsGroupsHeader(r.Header.Get(HeaderWindowsGroups))
		user, err := s.AuthenticateWindowsIdentityWithPassword(r.Context(), winUser, basicPass, groups)
		if user != nil {
			winUser = user.Username
		}
		return user, winUser, true, err
	}

	if !hasBasic || strings.TrimSpace(basicUser) == "" {
		return nil, "", false, fmt.Errorf("BasicAuth credentials required")
	}

	user, err := s.AuthenticateUser(r.Context(), basicUser, basicPass)
	if err != nil && strings.Contains(basicUser, `\`) && basicPass != "" {
		if winUser, winErr := s.AuthenticateWindowsIdentityWithPassword(r.Context(), basicUser, basicPass, nil); winErr == nil {
			return winUser, winUser.Username, true, nil
		}
	}
	return user, basicUser, true, err
}

func parseDBBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int64:
		return val != 0
	case int:
		return val != 0
	case []byte:
		s := strings.ToLower(strings.TrimSpace(string(val)))
		return s == "1" || s == "true" || s == "t" || s == "y" || s == "yes"
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		return s == "1" || s == "true" || s == "t" || s == "y" || s == "yes"
	default:
		return false
	}
}

// RequirePermission wraps an http.HandlerFunc with authentication against TableAPIUsers
// (supporting both BasicAuth and Windows Integrated user/group matching) and enforces
// that the authenticated user holds the required permission scope.
func (s *APIServer) RequirePermission(requiredPerm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now().UTC()
		user, attemptedUser, hasCreds, err := s.AuthenticateRequest(r)
		if !hasCreds {
			w.Header().Set("WWW-Authenticate", `Basic realm="go-flowAPI"`)
			s.logAPIEvent(r.Context(), APIAuditEvent{
				OccurredAt:   start,
				EventType:    "auth.missing_credentials",
				HTTPMethod:   r.Method,
				Path:         r.URL.Path,
				AuthUser:     attemptedUser,
				RemoteAddr:   r.RemoteAddr,
				StatusCode:   http.StatusUnauthorized,
				Status:       "UNAUTHORIZED",
				DurationMs:   time.Since(start).Milliseconds(),
				ErrorMessage: "Authentication credentials required",
			})
			writeJSONError(w, http.StatusUnauthorized, "Authentication credentials required")
			return
		}

		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="go-flowAPI"`)
			s.logAPIEvent(r.Context(), APIAuditEvent{
				OccurredAt:   start,
				EventType:    "auth.failed",
				HTTPMethod:   r.Method,
				Path:         r.URL.Path,
				AuthUser:     attemptedUser,
				RemoteAddr:   r.RemoteAddr,
				StatusCode:   http.StatusUnauthorized,
				Status:       "UNAUTHORIZED",
				DurationMs:   time.Since(start).Milliseconds(),
				ErrorMessage: err.Error(),
			})
			writeJSONError(w, http.StatusUnauthorized, err.Error())
			return
		}

		if !user.HasPermission(requiredPerm) {
			s.logAPIEvent(r.Context(), APIAuditEvent{
				OccurredAt:   start,
				EventType:    "auth.forbidden",
				HTTPMethod:   r.Method,
				Path:         r.URL.Path,
				AuthUser:     user.Username,
				RemoteAddr:   r.RemoteAddr,
				StatusCode:   http.StatusForbidden,
				Status:       "FORBIDDEN",
				DurationMs:   time.Since(start).Milliseconds(),
				ErrorMessage: fmt.Sprintf("user %q (permission=%q) lacks required permission %q", user.Username, user.Permission, requiredPerm),
			})
			writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Insufficient permissions: endpoint requires %q permission (user has %q)", requiredPerm, user.Permission))
			return
		}

		ctx := context.WithValue(r.Context(), authUserContextKey, user)
		next(w, r.WithContext(ctx))
	}
}
