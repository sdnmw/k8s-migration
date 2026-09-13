package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/transform"
)

type stubTransformService struct {
	profileID uuid.UUID
	manifest  []byte
	result    transform.Result
	err       error
}

func (s *stubTransformService) Preview(_ context.Context, profileID uuid.UUID, manifest []byte) (transform.Result, error) {
	s.profileID, s.manifest = profileID, manifest
	return s.result, s.err
}

func TestPreviewTransformAcceptsMultipartAndDoesNotExposeInternalObject(t *testing.T) {
	profileID := uuid.New()
	service := &stubTransformService{result: transform.Result{Changed: 1, Documents: []transform.Document{{
		APIVersion: "v1", Kind: "Secret", Namespace: "business-prod", Name: "database", Changed: true,
		SourceYAML: "data:\n  password: <redacted>\n", TargetYAML: "data:\n  password: <redacted>\n", UnifiedDiff: "--- source\n+++ target\n",
	}}}}
	recorder := httptest.NewRecorder()
	request := multipartTransformRequest(t, profileID.String(), "apiVersion: v1\nkind: Secret\nmetadata:\n  name: database\ndata:\n  password: c2VjcmV0\n")
	previewTransformHandler(Dependencies{Transforms: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.profileID != profileID || !bytes.Contains(service.manifest, []byte("kind: Secret")) {
		t.Fatalf("status=%d profile=%s body=%s", recorder.Code, service.profileID, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "c2VjcmV0") || strings.Contains(recorder.Body.String(), "object") {
		t.Fatalf("response leaked secret or internal object: %s", recorder.Body.String())
	}
}

func TestPreviewTransformMapsInvalidAndMissingProfileErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		profileID string
		err       error
		status    int
		code      string
	}{
		{name: "invalid id", profileID: "bad", status: http.StatusBadRequest, code: "TRANSFORM_INVALID"},
		{name: "missing profile", profileID: uuid.NewString(), err: repository.ErrNotFound, status: http.StatusNotFound, code: "MAPPING_NOT_FOUND"},
		{name: "invalid yaml", profileID: uuid.NewString(), err: transform.ErrInvalidManifest, status: http.StatusBadRequest, code: "TRANSFORM_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &stubTransformService{err: test.err}
			recorder := httptest.NewRecorder()
			previewTransformHandler(Dependencies{Transforms: service}).ServeHTTP(recorder, multipartTransformRequest(t, test.profileID, "x"))
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func multipartTransformRequest(t *testing.T, profileID, manifest string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("profileId", profileID); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("manifests", "resources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transforms/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
