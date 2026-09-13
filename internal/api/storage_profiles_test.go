package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/domain/storage"
	"github.com/smartx/sks-migration-center/internal/storageprofile"
)

type storageProfileServiceStub struct {
	input  storageprofile.Input
	value  storage.Profile
	result storageprofile.TestResult
}

func (s *storageProfileServiceStub) Create(_ context.Context, input storageprofile.Input) (storage.Profile, error) {
	s.input = input
	return s.value, nil
}
func (s *storageProfileServiceStub) List(context.Context, *uuid.UUID) ([]storage.Profile, error) {
	return []storage.Profile{s.value}, nil
}
func (s *storageProfileServiceStub) Install(context.Context, uuid.UUID) (storageprofile.TestResult, error) {
	return s.result, nil
}
func (s *storageProfileServiceStub) Test(context.Context, uuid.UUID) (storageprofile.TestResult, error) {
	return s.result, nil
}

func TestCreateStorageProfileDecodesExternalNFS(t *testing.T) {
	environmentID := uuid.New()
	service := &storageProfileServiceStub{value: storage.Profile{ID: uuid.New(), EnvironmentID: environmentID, Status: storage.StatusPending}}
	body := `{"environmentId":"` + environmentID.String() + `","name":"migration-nfs","type":"EXTERNAL_NFS_SC","storageClassName":"migration-nfs","nfsServer":"10.0.0.20","nfsExport":"/migration","reclaimPolicy":"Retain"}`
	recorder := httptest.NewRecorder()
	createStorageProfileHandler(Dependencies{StorageProfiles: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || service.input.NFSServer != "10.0.0.20" || service.input.ReclaimPolicy != storage.ReclaimRetain {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.input, recorder.Body.String())
	}
}

func TestInstallStorageProfileReturnsCompletedProbe(t *testing.T) {
	profileID := uuid.New()
	service := &storageProfileServiceStub{result: storageprofile.TestResult{
		Profile: storage.Profile{ID: profileID, Status: storage.StatusReady},
		Probe:   kubernetes.StorageProbeResult{StorageClass: "migration-nfs", PVCName: "probe", Bytes: 128, Remounted: true},
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/storage-profiles/"+profileID.String()+"/install", nil)
	request.SetPathValue("profileId", profileID.String())
	installStorageProfileHandler(Dependencies{StorageProfiles: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"remounted":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
