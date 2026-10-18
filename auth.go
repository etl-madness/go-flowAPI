package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
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
//   - "execution-only" (or "execution_only", "execute"): can execute/validate pipelines
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
			// Both "read-only" and "write"/"upload" can read artifacts
			if tok == PermReadOnly || tok == PermWrite || tok == PermUpload {
				return true
			}
		case PermWrite, PermUpload:
			if tok == PermWrite || tok == PermUpload {
				return true
			}
		case PermExecutionOnly:
			if tok == PermExecutionOnly {
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
	case "write", "upload", "write-only", "write_only", "upload-only", "upload_only", "read-write", "editor":
		return PermWrite
	default:
		return s
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
	if stored == "" {
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

// RequirePermission wraps an http.HandlerFunc with BasicAuth verification against TableAPIUsers
// and enforces that the authenticated user holds the required permission scope.
func (s *APIServer) RequirePermission(requiredPerm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now().UTC()
		username, password, ok := r.BasicAuth()
		if !ok || strings.TrimSpace(username) == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="go-flowAPI"`)
			s.logAPIEvent(r.Context(), APIAuditEvent{
				OccurredAt:   start,
				EventType:    "auth.missing_credentials",
				HTTPMethod:   r.Method,
				Path:         r.URL.Path,
				AuthUser:     username,
				RemoteAddr:   r.RemoteAddr,
				StatusCode:   http.StatusUnauthorized,
				Status:       "UNAUTHORIZED",
				DurationMs:   time.Since(start).Milliseconds(),
				ErrorMessage: "BasicAuth credentials required",
			})
			writeJSONError(w, http.StatusUnauthorized, "Basic authentication credentials required")
			return
		}

		user, err := s.AuthenticateUser(r.Context(), username, password)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="go-flowAPI"`)
			s.logAPIEvent(r.Context(), APIAuditEvent{
				OccurredAt:   start,
				EventType:    "auth.failed",
				HTTPMethod:   r.Method,
				Path:         r.URL.Path,
				AuthUser:     username,
				RemoteAddr:   r.RemoteAddr,
				StatusCode:   http.StatusUnauthorized,
				Status:       "UNAUTHORIZED",
				DurationMs:   time.Since(start).Milliseconds(),
				ErrorMessage: "Invalid username or password",
			})
			writeJSONError(w, http.StatusUnauthorized, "Invalid username or password")
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
