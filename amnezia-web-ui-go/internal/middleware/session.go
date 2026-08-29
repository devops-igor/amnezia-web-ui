package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

const (
	// SessionCookieName is the standard cookie name for authenticated sessions.
	SessionCookieName = "session"
	// DefaultSessionMaxAge is 7 days in seconds.
	DefaultSessionMaxAge = 86400 * 7
)

// UserLookupFunc defines a signature for verifying if a user exists and is active in the database.
type UserLookupFunc func(ctx context.Context, userID string) (*models.User, error)

var (
	userLookupMu sync.RWMutex
	userLookupFn UserLookupFunc
)

// SetUserLookup registers the user lookup callback used by auth middlewares to verify active status.
func SetUserLookup(fn UserLookupFunc) {
	userLookupMu.Lock()
	userLookupFn = fn
	userLookupMu.Unlock()
}

func checkUserActive(ctx context.Context, userID string) (*models.User, bool) {
	userLookupMu.RLock()
	fn := userLookupFn
	userLookupMu.RUnlock()
	if fn == nil {
		return nil, true
	}
	u, err := fn(ctx, userID)
	if err != nil || u == nil || !u.Enabled {
		return nil, false
	}
	return u, true
}

// Session creates a middleware that decodes and validates signed session cookies.
func Session(secretKey string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err == nil && cookie.Value != "" {
				if dataMap, err := security.DecodeSession(cookie.Value, secretKey); err == nil {
					sessionData := models.SessionDataFromMap(dataMap)
					if sessionData != nil && sessionData.IsAuthenticated() {
						r = r.WithContext(WithSession(r.Context(), sessionData))
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SetSessionCookie serializes and signs session data into an HTTP cookie.
func SetSessionCookie(w http.ResponseWriter, session *models.SessionData, secretKey string, secure bool, maxAge int) error {
	if maxAge <= 0 {
		maxAge = DefaultSessionMaxAge
	}

	encoded, err := security.EncodeSession(session.ToMap(), secretKey)
	if err != nil {
		return err
	}

	// #nosec G124 -- Session cookie configured with SameSite and configurable Secure flag
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    encoded,
		Path:     "/",
		MaxAge:   maxAge,
		Expires:  time.Now().Add(time.Duration(maxAge) * time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// ClearSessionCookie invalidates the active session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	// #nosec G124 -- Clearing session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// RequireAuth enforces that a valid user session is present and active in DB.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := GetSession(r.Context())
		if session == nil || !session.IsAuthenticated() {
			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		dbUser, active := checkUserActive(r.Context(), session.UserID)
		if !active {
			ClearSessionCookie(w)
			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "User account disabled or deleted")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if dbUser != nil {
			session.Role = dbUser.Role
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAdmin enforces that the authenticated user has the Admin role and is active in DB.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := GetSession(r.Context())
		if session == nil || !session.IsAuthenticated() {
			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		dbUser, active := checkUserActive(r.Context(), session.UserID)
		if !active {
			ClearSessionCookie(w)
			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "User account disabled or deleted")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if dbUser != nil {
			session.Role = dbUser.Role
		}

		if !session.IsAdmin() {
			WriteJSONError(w, http.StatusForbidden, "forbidden", "Admin privileges required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminOrSupport enforces that the authenticated user has either Admin or Support role and is active in DB.
func RequireAdminOrSupport(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := GetSession(r.Context())
		if session == nil || !session.IsAuthenticated() {
			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		dbUser, active := checkUserActive(r.Context(), session.UserID)
		if !active {
			ClearSessionCookie(w)
			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "User account disabled or deleted")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if dbUser != nil {
			session.Role = dbUser.Role
		}

		if !session.IsAdminOrSupport() {
			WriteJSONError(w, http.StatusForbidden, "forbidden", "Admin or support privileges required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
