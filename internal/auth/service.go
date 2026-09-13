package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/security"
)

var (
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrInvalidCSRF        = errors.New("invalid CSRF token")
	ErrWeakPassword       = errors.New("new password must contain at least 12 characters")
	ErrPasswordUnchanged  = errors.New("new password must differ from current password")
)

type LoginResult struct {
	Administrator identity.Administrator
	SessionToken  string
	CSRFToken     string
	ExpiresAt     time.Time
}

type Service struct {
	repository repository.IdentityRepository
	sessionTTL time.Duration
	now        func() time.Time
	dummyHash  string
}

func NewService(store repository.IdentityRepository, sessionTTL time.Duration) (*Service, error) {
	if store == nil || sessionTTL <= 0 {
		return nil, errors.New("identity repository and positive session TTL are required")
	}
	dummyHash, err := security.HashPassword("invalid-login-timing-padding")
	if err != nil {
		return nil, fmt.Errorf("create login timing hash: %w", err)
	}
	return &Service{repository: store, sessionTTL: sessionTTL, now: time.Now, dummyHash: dummyHash}, nil
}

func (s *Service) EnsureAdministrator(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("administrator username is required")
	}
	count, err := s.repository.AdministratorCount(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	err = s.repository.CreateAdministrator(ctx, identity.Administrator{
		ID: uuid.New(), Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now,
	})
	if errors.Is(err, repository.ErrConflict) {
		return nil
	}
	return err
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	username = strings.TrimSpace(username)
	administrator, err := s.repository.FindAdministratorByUsername(ctx, username)
	if errors.Is(err, repository.ErrNotFound) {
		_, _ = security.VerifyPassword(password, s.dummyHash)
		_ = s.recordLoginAudit(ctx, username, "FAILURE")
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}
	valid, err := security.VerifyPassword(password, administrator.PasswordHash)
	if err != nil || !valid {
		_ = s.recordLoginAudit(ctx, username, "FAILURE")
		return LoginResult{}, ErrInvalidCredentials
	}
	sessionToken, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	session := identity.Session{
		ID:              uuid.New(),
		AdministratorID: administrator.ID,
		TokenHash:       tokenHash(sessionToken),
		CSRFHash:        tokenHash(csrfToken),
		ExpiresAt:       now.Add(s.sessionTTL),
		CreatedAt:       now,
		LastSeenAt:      now,
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return LoginResult{}, err
	}
	if err := s.recordLoginAudit(ctx, administrator.Username, "SUCCESS"); err != nil {
		_ = s.repository.DeleteSessionByTokenHash(ctx, session.TokenHash)
		return LoginResult{}, err
	}
	administrator.PasswordHash = ""
	return LoginResult{Administrator: administrator, SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: session.ExpiresAt}, nil
}

func (s *Service) Authenticate(ctx context.Context, sessionToken string) (identity.Principal, error) {
	if sessionToken == "" {
		return identity.Principal{}, ErrUnauthenticated
	}
	principal, err := s.repository.FindPrincipalByTokenHash(ctx, tokenHash(sessionToken))
	if errors.Is(err, repository.ErrNotFound) {
		return identity.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return identity.Principal{}, err
	}
	return principal, nil
}

func (s *Service) ValidateCSRF(principal identity.Principal, cookieToken, headerToken string) error {
	if cookieToken == "" || headerToken == "" ||
		subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 ||
		subtle.ConstantTimeCompare(tokenHash(headerToken), principal.CSRFHash) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

func (s *Service) Logout(ctx context.Context, principal identity.Principal, sessionToken string) error {
	if err := s.repository.DeleteSessionByTokenHash(ctx, tokenHash(sessionToken)); err != nil {
		return err
	}
	return s.repository.RecordAudit(ctx, identity.AuditEvent{
		Actor: principal.Administrator.Username, Action: "AUTH_LOGOUT", ObjectType: "SESSION",
		ObjectID: &principal.SessionID, Result: "SUCCESS", Detail: map[string]any{},
	})
}

func (s *Service) ChangePassword(ctx context.Context, principal identity.Principal, currentPassword, newPassword string) error {
	if len(newPassword) < 12 {
		return ErrWeakPassword
	}
	administrator, err := s.repository.FindAdministratorByUsername(ctx, principal.Administrator.Username)
	if err != nil {
		return err
	}
	valid, err := security.VerifyPassword(currentPassword, administrator.PasswordHash)
	if err != nil || !valid || administrator.ID != principal.Administrator.ID {
		return ErrInvalidCredentials
	}
	unchanged, err := security.VerifyPassword(newPassword, administrator.PasswordHash)
	if err != nil {
		return err
	}
	if unchanged {
		return ErrPasswordUnchanged
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return ErrWeakPassword
	}
	if err := s.repository.ChangeAdministratorPassword(ctx, administrator.ID, hash, s.now().UTC()); err != nil {
		return err
	}
	_ = s.repository.RecordAudit(ctx, identity.AuditEvent{
		Actor: administrator.Username, Action: "AUTH_PASSWORD_CHANGED", ObjectType: "ADMINISTRATOR",
		ObjectID: &administrator.ID, Result: "SUCCESS", Detail: map[string]any{"sessionsInvalidated": true},
	})
	return nil
}

func (s *Service) recordLoginAudit(ctx context.Context, actor, result string) error {
	return s.repository.RecordAudit(ctx, identity.AuditEvent{
		Actor: actor, Action: "AUTH_LOGIN", ObjectType: "ADMINISTRATOR", Result: result,
		Detail: map[string]any{},
	})
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
