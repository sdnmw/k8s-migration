package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/objectstorage"
)

type objectStorageServiceStub struct {
	input      objectstorage.BootstrapInput
	adoptInput objectstorage.AdoptInput
	result     objectstorage.BootstrapResult
	err        error
}

func (s *objectStorageServiceStub) Bootstrap(_ context.Context, input objectstorage.BootstrapInput) (objectstorage.BootstrapResult, error) {
	s.input = input
	return s.result, s.err
}
func (s *objectStorageServiceStub) Adopt(_ context.Context, input objectstorage.AdoptInput) (objectstorage.BootstrapResult, error) {
	s.adoptInput = input
	return s.result, s.err
}
func (s *objectStorageServiceStub) ConnectExternal(_ context.Context, _ objectstorage.ExternalInput) (objectstorage.ProfileResult, error) {
	return objectstorage.ProfileResult{Profile: s.result.Profile, Checks: s.result.Checks}, s.err
}
func (s *objectStorageServiceStub) Test(context.Context, uuid.UUID) ([]objectstorage.Check, error) {
	return []objectstorage.Check{{Name: "S3", Status: "PASSED"}}, s.err
}
func (s *objectStorageServiceStub) Profiles(context.Context) ([]platform.ObjectStorageProfile, error) {
	return []platform.ObjectStorageProfile{{ID: uuid.New(), Name: "managed-minio", Bucket: "velero"}}, s.err
}
func (s *objectStorageServiceStub) Addons(_ context.Context, environmentID uuid.UUID) ([]platform.AddonInstallation, error) {
	return []platform.AddonInstallation{{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonMinIO, Version: "safe", Status: platform.InstallationReady}}, s.err
}
func (s *objectStorageServiceStub) Policy() objectstorage.SourcePolicy {
	return objectstorage.SourcePolicy{Ref: "blocked", ReleaseAllowed: false, SecurityGate: objectstorage.SecurityGate{Status: "BLOCKED", CheckedAt: time.Unix(1, 0)}}
}

func TestBootstrapObjectStorageDecodesLockedInputs(t *testing.T) {
	environmentID := uuid.New()
	service := &objectStorageServiceStub{result: objectstorage.BootstrapResult{
		Installation: platform.AddonInstallation{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonMinIO, Status: platform.InstallationReady},
		Profile:      platform.ObjectStorageProfile{ID: uuid.New(), Bucket: "velero"},
	}}
	body := `{"environmentId":"` + environmentID.String() + `","endpoint":"https://minio.test","storageClass":"smtx-block","imageRepository":"harbor/minio","imageDigest":"sha256:` + strings.Repeat("a", 64) + `","tlsSecretName":"minio-tls"}`
	recorder := httptest.NewRecorder()
	bootstrapObjectStorageHandler(Dependencies{ObjectStorage: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/object-storage/bootstrap", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || service.input.EnvironmentID != environmentID || service.input.StorageClass != "smtx-block" {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.input, recorder.Body.String())
	}
}

func TestBootstrapObjectStorageReportsSecurityGate(t *testing.T) {
	service := &objectStorageServiceStub{err: fmt.Errorf("%w: GHSA-test", objectstorage.ErrReleaseBlocked)}
	recorder := httptest.NewRecorder()
	bootstrapObjectStorageHandler(Dependencies{ObjectStorage: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"environmentId":"`+uuid.NewString()+`"}`)))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "MINIO_RELEASE_BLOCKED") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdoptObjectStorageDecodesExistingInstanceInput(t *testing.T) {
	environmentID := uuid.New()
	service := &objectStorageServiceStub{result: objectstorage.BootstrapResult{
		Installation: platform.AddonInstallation{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonMinIO, Status: platform.InstallationReady},
		Profile:      platform.ObjectStorageProfile{ID: uuid.New(), Bucket: "velero"},
	}}
	body := `{"environmentId":"` + environmentID.String() + `","endpoint":"https://minio.test","bucket":"velero","tlsSecretName":"minio-tls"}`
	recorder := httptest.NewRecorder()
	adoptObjectStorageHandler(Dependencies{ObjectStorage: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/object-storage/minio/adopt", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || service.adoptInput.EnvironmentID != environmentID || service.adoptInput.Endpoint != "https://minio.test" {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.adoptInput, recorder.Body.String())
	}
}

func TestConnectExternalObjectStorageReturnsCreatedProfile(t *testing.T) {
	profileID := uuid.New()
	service := &objectStorageServiceStub{result: objectstorage.BootstrapResult{Profile: platform.ObjectStorageProfile{ID: profileID, Name: "external-s3", Bucket: "velero"}}}
	body := `{"name":"external-s3","endpoint":"https://s3.test","bucket":"velero","accessKey":"access","secretKey":"secret"}`
	recorder := httptest.NewRecorder()
	connectExternalObjectStorageHandler(Dependencies{ObjectStorage: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/object-storage/profiles", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), profileID.String()) || strings.Contains(recorder.Body.String(), "secretKey") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestObjectStorageTestRejectsMissingProfile(t *testing.T) {
	service := &objectStorageServiceStub{err: errors.New("should not be called")}
	recorder := httptest.NewRecorder()
	testObjectStorageHandler(Dependencies{ObjectStorage: service}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListObjectStorageProfiles(t *testing.T) {
	recorder := httptest.NewRecorder()
	listObjectStorageProfilesHandler(Dependencies{ObjectStorage: &objectStorageServiceStub{}}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "managed-minio") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
