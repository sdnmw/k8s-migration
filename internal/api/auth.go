package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/smartx/sks-migration-center/internal/auth"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
)

const (
	sessionCookieName = "sks_migration_session"
	csrfCookieName    = "sks_migration_csrf"
	csrfHeaderName    = "X-CSRF-Token"
)

type principalContextKey struct{}

type CookieConfig struct {
	Secure bool
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type administratorResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func loginHandler(service *auth.Service, cookies CookieConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeInternalProblem(w)
			return
		}
		var request loginRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Username) == "" || len(request.Password) < 1 {
			writeProblem(w, problem{
				Type: "/problems/invalid-request", Title: "Invalid request", Status: http.StatusBadRequest,
				Detail: "A username and password are required.", Code: "REQUEST_INVALID",
			})
			return
		}
		result, err := service.Login(r.Context(), request.Username, request.Password)
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeProblem(w, problem{
				Type: "/problems/invalid-credentials", Title: "Authentication failed", Status: http.StatusUnauthorized,
				Detail: "The username or password is incorrect.", Code: "AUTH_INVALID_CREDENTIALS",
			})
			return
		}
		if err != nil {
			writeInternalProblem(w)
			return
		}
		setAuthCookies(w, result.SessionToken, result.CSRFToken, result.ExpiresAt, cookies.Secure)
		w.Header().Set(csrfHeaderName, result.CSRFToken)
		w.WriteHeader(http.StatusNoContent)
	}
}

func meHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			writeUnauthorizedProblem(w)
			return
		}
		writeJSON(w, http.StatusOK, administratorResponse{
			ID: principal.Administrator.ID.String(), Username: principal.Administrator.Username,
		})
	}
}

func logoutHandler(service *auth.Service, cookies CookieConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			writeUnauthorizedProblem(w)
			return
		}
		sessionCookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeUnauthorizedProblem(w)
			return
		}
		if err := service.Logout(r.Context(), principal, sessionCookie.Value); err != nil {
			writeInternalProblem(w)
			return
		}
		clearAuthCookies(w, cookies.Secure)
		w.WriteHeader(http.StatusNoContent)
	}
}

func changePasswordHandler(service *auth.Service, cookies CookieConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok {
			writeUnauthorizedProblem(w)
			return
		}
		var request changePasswordRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.CurrentPassword == "" || request.NewPassword == "" {
			writeProblem(w, problem{Type: "/problems/invalid-request", Title: "Invalid request", Status: http.StatusBadRequest, Detail: "Current and new passwords are required.", Code: "REQUEST_INVALID"})
			return
		}
		err := service.ChangePassword(r.Context(), principal, request.CurrentPassword, request.NewPassword)
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeProblem(w, problem{Type: "/problems/current-password-invalid", Title: "Password change rejected", Status: http.StatusBadRequest, Detail: "The current password is incorrect.", Code: "AUTH_CURRENT_PASSWORD_INVALID"})
			return
		case errors.Is(err, auth.ErrWeakPassword):
			writeProblem(w, problem{Type: "/problems/password-policy", Title: "Password change rejected", Status: http.StatusBadRequest, Detail: "The new password must contain at least 12 characters.", Code: "AUTH_PASSWORD_TOO_SHORT"})
			return
		case errors.Is(err, auth.ErrPasswordUnchanged):
			writeProblem(w, problem{Type: "/problems/password-unchanged", Title: "Password change rejected", Status: http.StatusBadRequest, Detail: "The new password must differ from the current password.", Code: "AUTH_PASSWORD_UNCHANGED"})
			return
		case err != nil:
			writeInternalProblem(w)
			return
		}
		clearAuthCookies(w, cookies.Secure)
		w.WriteHeader(http.StatusNoContent)
	}
}

func requireAuthentication(service *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeInternalProblem(w)
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeUnauthorizedProblem(w)
			return
		}
		principal, err := service.Authenticate(r.Context(), cookie.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeUnauthorizedProblem(w)
			return
		}
		if err != nil {
			writeInternalProblem(w)
			return
		}
		if requiresCSRF(r.Method) {
			csrfCookie, cookieErr := r.Cookie(csrfCookieName)
			if cookieErr != nil || service.ValidateCSRF(principal, cookieValue(csrfCookie), r.Header.Get(csrfHeaderName)) != nil {
				writeProblem(w, problem{
					Type: "/problems/invalid-csrf", Title: "Request rejected", Status: http.StatusForbidden,
					Detail: "The CSRF token is missing or invalid.", Code: "AUTH_CSRF_INVALID",
				})
				return
			}
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func principalFromContext(ctx context.Context) (identity.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(identity.Principal)
	return principal, ok
}

func requiresCSRF(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func cookieValue(cookie *http.Cookie) string {
	if cookie == nil {
		return ""
	}
	return cookie.Value
}

func setAuthCookies(w http.ResponseWriter, sessionToken, csrfToken string, expiresAt time.Time, secure bool) {
	maxAge := int(time.Until(expiresAt).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: sessionToken, Path: "/", Expires: expiresAt, MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: csrfToken, Path: "/", Expires: expiresAt, MaxAge: maxAge,
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func clearAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
			HttpOnly: name == sessionCookieName, Secure: secure, SameSite: http.SameSiteStrictMode,
		})
	}
}

func writeUnauthorizedProblem(w http.ResponseWriter) {
	writeProblem(w, problem{
		Type: "/problems/authentication-required", Title: "Authentication required", Status: http.StatusUnauthorized,
		Detail: "Sign in as the local administrator.", Code: "AUTH_REQUIRED",
	})
}

func writeInternalProblem(w http.ResponseWriter) {
	writeProblem(w, problem{
		Type: "/problems/internal", Title: "Internal server error", Status: http.StatusInternalServerError,
		Detail: "The request could not be completed.", Code: "INTERNAL_ERROR",
	})
}
