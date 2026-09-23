package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/auth"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	environmentservice "github.com/smartx/sks-migration-center/internal/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type stubEnvironmentService struct {
	created environmentservice.CreateInput
	value   domainenvironment.Environment
	err     error
}

func TestDeleteEnvironmentExplainsMigrationPlanReference(t *testing.T) {
	service := &stubEnvironmentService{err: repository.ErrConflict}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/environments/{environmentId}", deleteEnvironmentHandler(Dependencies{Environments: service}))
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/environments/"+uuid.NewString(), nil)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var value problem
	if err := json.NewDecoder(recorder.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	if value.Code != "ENVIRONMENT_IN_USE" || !strings.Contains(value.Detail, "迁移计划引用") {
		t.Fatalf("unexpected problem response: %+v", value)
	}
}

func (s *stubEnvironmentService) Create(_ context.Context, input environmentservice.CreateInput) (domainenvironment.Environment, error) {
	s.created = input
	return s.value, s.err
}
func (s *stubEnvironmentService) Get(context.Context, uuid.UUID) (domainenvironment.Environment, error) {
	return s.value, s.err
}
func (s *stubEnvironmentService) List(context.Context) ([]domainenvironment.Environment, error) {
	return []domainenvironment.Environment{s.value}, s.err
}
func (s *stubEnvironmentService) Delete(context.Context, uuid.UUID) error { return s.err }
func (s *stubEnvironmentService) TestConnection(context.Context, uuid.UUID) (domainenvironment.ConnectionTest, error) {
	return domainenvironment.ConnectionTest{Success: true}, s.err
}
func (s *stubEnvironmentService) Capabilities(context.Context, uuid.UUID) (domainenvironment.Capabilities, error) {
	return s.value.Capabilities, s.err
}
func (s *stubEnvironmentService) RefreshCapabilities(context.Context, uuid.UUID) (domainenvironment.Capabilities, error) {
	return s.value.Capabilities, s.err
}
func (s *stubEnvironmentService) Namespaces(context.Context, uuid.UUID) ([]string, error) {
	return []string{"default"}, s.err
}

func TestCreateEnvironmentIsWriteOnlyForKubeconfigAndCredentialID(t *testing.T) {
	credentialID := uuid.New()
	now := time.Now().UTC()
	service := &stubEnvironmentService{value: domainenvironment.Environment{
		ID: uuid.New(), Name: "SKS Production", Role: domainenvironment.RoleTarget, Kind: domainenvironment.KindKubernetes,
		Endpoint: "https://sks.example.test:6443", CredentialID: &credentialID, Status: domainenvironment.StatusPending,
		Capabilities: domainenvironment.Capabilities{}, CreatedAt: now, UpdatedAt: now,
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/environments", createEnvironmentHandler(Dependencies{Environments: service}))
	body := `{"name":"SKS Production","role":"TARGET","kind":"KUBERNETES","credential":"sensitive-kubeconfig"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/environments", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if string(service.created.Credential) != "sensitive-kubeconfig" {
		t.Fatal("credential was not passed to the service")
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("credential")) || bytes.Contains(recorder.Body.Bytes(), []byte("sensitive")) {
		t.Fatalf("credential leaked in response: %s", recorder.Body.String())
	}
}

func TestCreateEnvironmentRejectsUnknownFields(t *testing.T) {
	service := &stubEnvironmentService{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/environments", strings.NewReader(
		`{"name":"source","role":"SOURCE","kind":"KUBERNETES","credential":"value","password":"leak"}`,
	))
	recorder := httptest.NewRecorder()
	createEnvironmentHandler(Dependencies{Environments: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
	var value problem
	if err := json.NewDecoder(recorder.Body).Decode(&value); err != nil || value.Code != "ENVIRONMENT_INVALID" {
		t.Fatalf("unexpected problem response: %+v err=%v", value, err)
	}
}

func TestEnvironmentRoutesRequireAuthenticationAndCSRF(t *testing.T) {
	identityStore := newAPIIdentityRepository()
	authentication, _ := auth.NewService(identityStore, time.Hour)
	const password = "correct horse battery staple"
	if err := authentication.EnsureAdministrator(context.Background(), "admin", password); err != nil {
		t.Fatalf("bootstrap administrator: %v", err)
	}
	service := &stubEnvironmentService{value: domainenvironment.Environment{
		ID: uuid.New(), Name: "source", Role: domainenvironment.RoleSource, Kind: domainenvironment.KindKubernetes,
		Status: domainenvironment.StatusPending, Capabilities: domainenvironment.Capabilities{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}
	router := NewRouter(Dependencies{Auth: authentication, Environments: service, Audit: identityStore})

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status=%d", unauthenticated.Code)
	}

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"username":"admin","password":"`+password+`"}`,
	)))
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	body := `{"name":"source","role":"SOURCE","kind":"KUBERNETES","credential":"safe-value"}`
	missingCSRF := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodPost, "/api/v1/environments", strings.NewReader(body))
	missingRequest.AddCookie(sessionCookie)
	missingRequest.AddCookie(csrfCookie)
	router.ServeHTTP(missingCSRF, missingRequest)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status=%d", missingCSRF.Code)
	}

	created := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/environments", strings.NewReader(body))
	createRequest.AddCookie(sessionCookie)
	createRequest.AddCookie(csrfCookie)
	createRequest.Header.Set(csrfHeaderName, csrfCookie.Value)
	router.ServeHTTP(created, createRequest)
	if created.Code != http.StatusCreated {
		t.Fatalf("authenticated create status=%d body=%s", created.Code, created.Body.String())
	}
	if len(identityStore.audits) < 2 || identityStore.audits[len(identityStore.audits)-1].Action != "environment.create" {
		t.Fatalf("environment action was not audited: %+v", identityStore.audits)
	}

	refresh := httptest.NewRecorder()
	refreshRequest := httptest.NewRequest(http.MethodPost, "/api/v1/environments/"+service.value.ID.String()+"/capabilities", nil)
	refreshRequest.AddCookie(sessionCookie)
	refreshRequest.AddCookie(csrfCookie)
	refreshRequest.Header.Set(csrfHeaderName, csrfCookie.Value)
	router.ServeHTTP(refresh, refreshRequest)
	if refresh.Code != http.StatusOK {
		t.Fatalf("capability refresh status=%d body=%s", refresh.Code, refresh.Body.String())
	}

	namespaces := httptest.NewRecorder()
	namespaceRequest := httptest.NewRequest(http.MethodGet, "/api/v1/environments/"+service.value.ID.String()+"/namespaces", nil)
	namespaceRequest.AddCookie(sessionCookie)
	router.ServeHTTP(namespaces, namespaceRequest)
	if namespaces.Code != http.StatusOK || !strings.Contains(namespaces.Body.String(), "default") {
		t.Fatalf("namespace list status=%d body=%s", namespaces.Code, namespaces.Body.String())
	}
}
