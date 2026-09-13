package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	veleroadapter "github.com/smartx/sks-migration-center/internal/adapter/velero"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	veleroservice "github.com/smartx/sks-migration-center/internal/velero"
)

type veleroAddonStub struct {
	input  veleroservice.InstallInput
	reuse  veleroservice.ReuseInput
	result veleroservice.InstallResult
	err    error
}

func (s *veleroAddonStub) Install(_ context.Context, input veleroservice.InstallInput) (veleroservice.InstallResult, error) {
	s.input = input
	return s.result, s.err
}

func (s *veleroAddonStub) Reuse(_ context.Context, input veleroservice.ReuseInput) (veleroservice.InstallResult, error) {
	s.reuse = input
	return s.result, s.err
}

func (s *veleroAddonStub) Status(context.Context, uuid.UUID) (veleroservice.InstallResult, error) {
	return s.result, nil
}
func (s *veleroAddonStub) Uninstall(context.Context, uuid.UUID) (platform.AddonInstallation, error) {
	return s.result.Installation, s.err
}

func TestInstallVeleroAddonUsesPathEnvironment(t *testing.T) {
	environmentID, profileID := uuid.New(), uuid.New()
	service := &veleroAddonStub{result: veleroservice.InstallResult{
		Installation: platform.AddonInstallation{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: veleroservice.VeleroVersion, Status: platform.InstallationReady},
		Location:     veleroadapter.BackupStorageLocationStatus{Name: veleroservice.BackupLocationName, Phase: "Available"},
	}}
	body := `{"type":"VELERO","objectStorageProfileId":"` + profileID.String() + `","prefix":"sida","kubeletRoot":"/var/lib/kubelet"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons/"+environmentID.String()+"/install", strings.NewReader(body))
	request.SetPathValue("environmentId", environmentID.String())
	recorder := httptest.NewRecorder()
	installAddonHandler(Dependencies{Velero: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || service.input.EnvironmentID != environmentID || service.input.ObjectStorageProfile != profileID {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.input, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"phase":"Available"`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestInstallAddonRejectsUnsupportedType(t *testing.T) {
	environmentID := uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/addons/"+environmentID.String()+"/install", strings.NewReader(`{"type":"NFS_CSI","objectStorageProfileId":"`+uuid.NewString()+`"}`))
	request.SetPathValue("environmentId", environmentID.String())
	recorder := httptest.NewRecorder()
	installAddonHandler(Dependencies{Velero: &veleroAddonStub{}}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "ADDON_INVALID") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInstallAddonReportsUnmanagedVeleroConflict(t *testing.T) {
	environmentID := uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"type":"VELERO","objectStorageProfileId":"`+uuid.NewString()+`"}`))
	request.SetPathValue("environmentId", environmentID.String())
	recorder := httptest.NewRecorder()
	installAddonHandler(Dependencies{Velero: &veleroAddonStub{err: veleroservice.ErrUnmanagedInstallation}}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "VELERO_EXISTING_UNMANAGED") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestReuseVeleroAddonUsesExistingInstallation(t *testing.T) {
	environmentID, profileID := uuid.New(), uuid.New()
	service := &veleroAddonStub{result: veleroservice.InstallResult{
		Installation: platform.AddonInstallation{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: "1.13.2", Status: platform.InstallationReady},
		Location:     veleroadapter.BackupStorageLocationStatus{Name: veleroservice.BackupLocationName, Phase: "Available"},
	}}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"objectStorageProfileId":"`+profileID.String()+`","prefix":"sida"}`))
	request.SetPathValue("environmentId", environmentID.String())
	recorder := httptest.NewRecorder()
	reuseVeleroHandler(Dependencies{Velero: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || service.reuse.EnvironmentID != environmentID || service.reuse.ObjectStorageProfile != profileID {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, service.reuse, recorder.Body.String())
	}
}

func TestUninstallVeleroAddon(t *testing.T) {
	environmentID := uuid.New()
	service := &veleroAddonStub{result: veleroservice.InstallResult{Installation: platform.AddonInstallation{ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonVelero, Version: veleroservice.VeleroVersion, Status: platform.InstallationRemoved}}}
	request := httptest.NewRequest(http.MethodDelete, "/", nil)
	request.SetPathValue("environmentId", environmentID.String())
	recorder := httptest.NewRecorder()
	uninstallAddonHandler(Dependencies{Velero: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"REMOVED"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
