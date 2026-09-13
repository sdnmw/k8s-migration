package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	composeanalyzer "github.com/smartx/sks-migration-center/internal/compose"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
)

func TestAnalyzeComposeMultipartDoesNotReturnEnvironmentValues(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("projectName", "factory")
	composePart, _ := writer.CreateFormFile("compose", "compose.yaml")
	_, _ = composePart.Write([]byte("services:\n  api:\n    image: registry.example/api:${TAG}\n    environment:\n      PASSWORD: ${PASSWORD}\n"))
	envPart, _ := writer.CreateFormFile("environment", ".env")
	_, _ = envPart.Write([]byte("TAG=v1\nPASSWORD=do-not-leak\n"))
	_ = writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/compose/analyze", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	analyzeComposeHandler(Dependencies{Compose: composeanalyzer.NewAnalyzer()}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "do-not-leak") || !strings.Contains(recorder.Body.String(), `"projectName":"factory"`) {
		t.Fatalf("unexpected or leaking response: %s", recorder.Body.String())
	}
}

func TestAnalyzeComposeRegistersApplicationWhenEnvironmentIsProvided(t *testing.T) {
	environmentID := uuid.New()
	applicationID := uuid.New()
	service := &stubApplicationService{value: domainapplication.SourceApplication{ID: applicationID, EnvironmentID: environmentID, Name: "factory", SourceType: domainapplication.SourceCompose}}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("projectName", "factory")
	_ = writer.WriteField("environmentId", environmentID.String())
	composePart, _ := writer.CreateFormFile("compose", "compose.yaml")
	_, _ = composePart.Write([]byte("services:\n  api:\n    image: registry.example/api:v1\n"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/compose/analyze", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	analyzeComposeHandler(Dependencies{Compose: composeanalyzer.NewAnalyzer(), Applications: service}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), applicationID.String()) || service.environment != environmentID {
		t.Fatalf("status=%d body=%s environment=%s", recorder.Code, recorder.Body.String(), service.environment)
	}
}

func TestAnalyzeComposeRejectsExternalFileReferences(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("compose", "compose.yaml")
	_, _ = part.Write([]byte("services:\n  api:\n    image: app\n    env_file: /etc/passwd\n"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/compose/analyze", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	analyzeComposeHandler(Dependencies{Compose: composeanalyzer.NewAnalyzer()}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "COMPOSE_INVALID") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
