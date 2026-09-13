package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	applicationservice "github.com/smartx/sks-migration-center/internal/application"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
)

func TestComposeDiscoveryErrorExplainsPermissionAndDoesNotLeakRawOutput(t *testing.T) {
	permission := applicationDiscoveryDetail(fmt.Errorf("%w: COMPOSE_CONFIG_PERMISSION", applicationservice.ErrDiscovery))
	if !strings.Contains(permission, "SSH 用户") || !strings.Contains(permission, "权限") {
		t.Fatal(permission)
	}
	unknown := applicationDiscoveryDetail(fmt.Errorf("%w: password=do-not-print", applicationservice.ErrDiscovery))
	if strings.Contains(unknown, "do-not-print") {
		t.Fatal("raw failure text leaked")
	}
}

type stubApplicationService struct {
	value       domainapplication.SourceApplication
	environment uuid.UUID
	namespace   string
	err         error
}

func (s *stubApplicationService) Discover(_ context.Context, environmentID uuid.UUID, namespace string) (domainapplication.SourceApplication, error) {
	s.environment, s.namespace = environmentID, namespace
	return s.value, s.err
}
func (s *stubApplicationService) DiscoverSelected(_ context.Context, environmentID uuid.UUID, namespace, _ string, _ []domainapplication.ResourceReference) (domainapplication.SourceApplication, error) {
	return s.Discover(context.Background(), environmentID, namespace)
}
func (s *stubApplicationService) DiscoverAll(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error) {
	return []domainapplication.SourceApplication{s.value}, s.err
}
func (s *stubApplicationService) DiscoverCompose(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error) {
	return []domainapplication.SourceApplication{s.value}, s.err
}
func (s *stubApplicationService) Preview(context.Context, uuid.UUID, string) (domainapplication.Inventory, error) {
	return s.value.Inventory, s.err
}
func (s *stubApplicationService) RegisterCompose(_ context.Context, environmentID uuid.UUID, _ string, _, _ []byte) (domainapplication.SourceApplication, error) {
	s.environment = environmentID
	return s.value, s.err
}
func (s *stubApplicationService) Get(context.Context, uuid.UUID) (domainapplication.SourceApplication, error) {
	return s.value, s.err
}
func (s *stubApplicationService) List(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error) {
	return []domainapplication.SourceApplication{s.value}, s.err
}

func TestDiscoverApplicationReturnsNormalizedInventory(t *testing.T) {
	environmentID := uuid.New()
	service := &stubApplicationService{value: domainapplication.SourceApplication{
		ID: uuid.New(), EnvironmentID: environmentID, Name: "business", Namespace: "business", SourceType: domainapplication.SourceKubernetes,
		Inventory: domainapplication.Inventory{Resources: []domainapplication.ResourceSummary{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "business", Name: "api"}}, Counts: map[string]int{"Deployment": 1}},
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/applications/discover", strings.NewReader(`{"environmentId":"`+environmentID.String()+`","namespace":"business"}`))
	recorder := httptest.NewRecorder()
	discoverApplicationHandler(Dependencies{Applications: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"Deployment":1`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.environment != environmentID || service.namespace != "business" {
		t.Fatalf("request was not forwarded: %+v", service)
	}
}

func TestDiscoverApplicationMapsValidationAndDiscoveryErrors(t *testing.T) {
	for name, testCase := range map[string]struct {
		service *stubApplicationService
		status  int
		code    string
	}{
		"invalid":   {service: &stubApplicationService{err: applicationservice.ErrInvalidInput}, status: http.StatusBadRequest, code: "APPLICATION_INVALID"},
		"discovery": {service: &stubApplicationService{err: applicationservice.ErrDiscovery}, status: http.StatusUnprocessableEntity, code: "APPLICATION_DISCOVERY_FAILED"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/applications/discover", strings.NewReader(`{"environmentId":"`+uuid.NewString()+`","namespace":"business"}`))
			recorder := httptest.NewRecorder()
			discoverApplicationHandler(Dependencies{Applications: testCase.service}).ServeHTTP(recorder, request)
			if recorder.Code != testCase.status || !strings.Contains(recorder.Body.String(), testCase.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
