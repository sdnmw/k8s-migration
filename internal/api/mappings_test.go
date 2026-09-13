package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type stubMappingService struct {
	value  domainmapping.Profile
	input  domainmapping.Profile
	target *uuid.UUID
	err    error
}

func (s *stubMappingService) Create(_ context.Context, value domainmapping.Profile) (domainmapping.Profile, error) {
	s.input = value
	return s.value, s.err
}
func (s *stubMappingService) Update(_ context.Context, _ uuid.UUID, value domainmapping.Profile) (domainmapping.Profile, error) {
	s.input = value
	return s.value, s.err
}
func (s *stubMappingService) Get(context.Context, uuid.UUID) (domainmapping.Profile, error) {
	return s.value, s.err
}
func (s *stubMappingService) List(_ context.Context, target *uuid.UUID) ([]domainmapping.Profile, error) {
	s.target = target
	return []domainmapping.Profile{s.value}, s.err
}
func (s *stubMappingService) Delete(context.Context, uuid.UUID) error { return s.err }

func TestCreateMappingDecodesAllMappingKinds(t *testing.T) {
	targetID := uuid.New()
	service := &stubMappingService{value: domainmapping.Profile{ID: uuid.New(), Name: "default", TargetEnvironmentID: targetID, Storage: []domainmapping.KeyValue{}, Namespaces: []domainmapping.KeyValue{}, Ingress: []domainmapping.KeyValue{}, Registries: []domainmapping.KeyValue{}, NFS: []domainmapping.NFSMapping{}, NodeLabels: []domainmapping.NodeLabelMapping{}}}
	body := `{"name":"default","targetEnvironmentId":"` + targetID.String() + `","storageMappings":[{"source":"old","target":"smtx-block"}],"namespaceMappings":[],"ingressMappings":[],"registryMappings":[],"nfsMappings":[],"nodeLabelMappings":[]}`
	recorder := httptest.NewRecorder()
	createMappingHandler(Dependencies{Mappings: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mapping-profiles", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || service.input.Storage[0].Target != "smtx-block" {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.input, recorder.Body.String())
	}
}

func TestListMappingsParsesTargetAndMapsConflict(t *testing.T) {
	targetID := uuid.New()
	service := &stubMappingService{}
	recorder := httptest.NewRecorder()
	listMappingsHandler(Dependencies{Mappings: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/mapping-profiles?targetEnvironmentId="+targetID.String(), nil))
	if recorder.Code != http.StatusOK || service.target == nil || *service.target != targetID {
		t.Fatalf("status=%d target=%v", recorder.Code, service.target)
	}
	service.err = repository.ErrConflict
	recorder = httptest.NewRecorder()
	createMappingHandler(Dependencies{Mappings: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mapping-profiles", strings.NewReader(`{"name":"x","targetEnvironmentId":"`+targetID.String()+`"}`)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "MAPPING_CONFLICT") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
