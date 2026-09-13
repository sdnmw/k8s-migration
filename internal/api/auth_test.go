package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/auth"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type apiIdentityRepository struct {
	administrator *identity.Administrator
	sessions      map[string]identity.Session
	audits        []identity.AuditEvent
}

func newAPIIdentityRepository() *apiIdentityRepository {
	return &apiIdentityRepository{sessions: map[string]identity.Session{}}
}

func (r *apiIdentityRepository) AdministratorCount(context.Context) (int, error) {
	if r.administrator == nil {
		return 0, nil
	}
	return 1, nil
}

func (r *apiIdentityRepository) CreateAdministrator(_ context.Context, value identity.Administrator) error {
	if r.administrator != nil {
		return repository.ErrConflict
	}
	r.administrator = &value
	return nil
}

func (r *apiIdentityRepository) ChangeAdministratorPassword(_ context.Context, administratorID uuid.UUID, passwordHash string, updatedAt time.Time) error {
	if r.administrator == nil || r.administrator.ID != administratorID {
		return repository.ErrNotFound
	}
	r.administrator.PasswordHash = passwordHash
	r.administrator.UpdatedAt = updatedAt
	for tokenHash, session := range r.sessions {
		if session.AdministratorID == administratorID {
			delete(r.sessions, tokenHash)
		}
	}
	return nil
}

func (r *apiIdentityRepository) FindAdministratorByUsername(_ context.Context, username string) (identity.Administrator, error) {
	if r.administrator == nil || r.administrator.Username != username {
		return identity.Administrator{}, repository.ErrNotFound
	}
	return *r.administrator, nil
}

func (r *apiIdentityRepository) CreateSession(_ context.Context, value identity.Session) error {
	r.sessions[string(value.TokenHash)] = value
	return nil
}

func (r *apiIdentityRepository) FindPrincipalByTokenHash(_ context.Context, hash []byte) (identity.Principal, error) {
	session, ok := r.sessions[string(hash)]
	if !ok || r.administrator == nil {
		return identity.Principal{}, repository.ErrNotFound
	}
	return identity.Principal{
		Administrator: *r.administrator,
		SessionID:     session.ID,
		CSRFHash:      session.CSRFHash,
		ExpiresAt:     session.ExpiresAt,
	}, nil
}

func (r *apiIdentityRepository) DeleteSessionByTokenHash(_ context.Context, hash []byte) error {
	delete(r.sessions, string(hash))
	return nil
}

func (r *apiIdentityRepository) DeleteExpiredSessions(context.Context) (int64, error) { return 0, nil }

func (r *apiIdentityRepository) RecordAudit(_ context.Context, event identity.AuditEvent) error {
	r.audits = append(r.audits, event)
	return nil
}

func TestAuthenticationCookiesCSRFAndLogout(t *testing.T) {
	store := newAPIIdentityRepository()
	service, err := auth.NewService(store, time.Hour)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	password := "correct horse battery staple"
	if err := service.EnsureAdministrator(context.Background(), "admin", password); err != nil {
		t.Fatalf("EnsureAdministrator: %v", err)
	}
	var logs bytes.Buffer
	router := NewRouter(Dependencies{
		Version: "test", Environment: "test", Auth: service, Cookies: CookieConfig{Secure: true},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated me status = %d", unauthenticated.Code)
	}

	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(login, request)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body=%s", login.Code, login.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		switch cookie.Name {
		case sessionCookieName:
			sessionCookie = cookie
		case csrfCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("invalid session cookie: %+v", sessionCookie)
	}
	if csrfCookie == nil || csrfCookie.HttpOnly || !csrfCookie.Secure || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("invalid CSRF cookie: %+v", csrfCookie)
	}

	me := httptest.NewRecorder()
	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meRequest.AddCookie(sessionCookie)
	router.ServeHTTP(me, meRequest)
	if me.Code != http.StatusOK || strings.Contains(me.Body.String(), "password") {
		t.Fatalf("me response = %d %s", me.Code, me.Body.String())
	}

	missingCSRF := httptest.NewRecorder()
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutRequest.AddCookie(csrfCookie)
	router.ServeHTTP(missingCSRF, logoutRequest)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d", missingCSRF.Code)
	}

	logout := httptest.NewRecorder()
	logoutRequest = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutRequest.AddCookie(csrfCookie)
	logoutRequest.Header.Set(csrfHeaderName, csrfCookie.Value)
	router.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d body=%s", logout.Code, logout.Body.String())
	}
	if strings.Contains(logs.String(), password) {
		t.Fatal("request logs contain the administrator password")
	}
}

func TestLoginRejectsUnknownJSONFields(t *testing.T) {
	service, _ := auth.NewService(newAPIIdentityRepository(), time.Hour)
	router := NewRouter(Dependencies{Auth: service, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"password-value","extra":"rejected"}`))
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("login with unknown field status = %d", response.Code)
	}
}

func TestChangePasswordRequiresCSRFAndInvalidatesSession(t *testing.T) {
	store := newAPIIdentityRepository()
	service, err := auth.NewService(store, time.Hour)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	oldPassword := "correct horse battery staple"
	newPassword := "new correct horse battery staple"
	if err := service.EnsureAdministrator(context.Background(), "admin", oldPassword); err != nil {
		t.Fatalf("EnsureAdministrator: %v", err)
	}
	var logs bytes.Buffer
	router := NewRouter(Dependencies{
		Version: "test", Environment: "test", Auth: service, Cookies: CookieConfig{Secure: true},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})

	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"`+oldPassword+`"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login status = %d body=%s", login.Code, login.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatal("login did not return authentication cookies")
	}

	missingCSRF := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"currentPassword":"`+oldPassword+`","newPassword":"`+newPassword+`"}`))
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	router.ServeHTTP(missingCSRF, request)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("password change without CSRF status = %d", missingCSRF.Code)
	}

	changed := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"currentPassword":"`+oldPassword+`","newPassword":"`+newPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(csrfHeaderName, csrfCookie.Value)
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	router.ServeHTTP(changed, request)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("password change status = %d body=%s", changed.Code, changed.Body.String())
	}
	expiredCookies := map[string]bool{}
	for _, cookie := range changed.Result().Cookies() {
		if cookie.MaxAge < 0 {
			expiredCookies[cookie.Name] = true
		}
	}
	if !expiredCookies[sessionCookieName] || !expiredCookies[csrfCookieName] {
		t.Fatalf("password change did not expire auth cookies: %+v", changed.Result().Cookies())
	}

	me := httptest.NewRecorder()
	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meRequest.AddCookie(sessionCookie)
	router.ServeHTTP(me, meRequest)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("old session after password change status = %d", me.Code)
	}
	oldLogin := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"`+oldPassword+`"}`))
	router.ServeHTTP(oldLogin, request)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d", oldLogin.Code)
	}
	newLogin := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"`+newPassword+`"}`))
	router.ServeHTTP(newLogin, request)
	if newLogin.Code != http.StatusNoContent {
		t.Fatalf("new password login status = %d body=%s", newLogin.Code, newLogin.Body.String())
	}
	if strings.Contains(logs.String(), oldPassword) || strings.Contains(logs.String(), newPassword) {
		t.Fatal("request logs contain an administrator password")
	}
}
