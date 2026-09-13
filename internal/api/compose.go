package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/compose"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
)

const maxComposeUploadBytes = compose.MaxComposeBytes + compose.MaxEnvBytes + 64*1024

type ComposeAnalyzer interface {
	Analyze(context.Context, string, []byte, []byte) (application.ComposeInventory, error)
}

func analyzeComposeHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Compose == nil {
			writeInternalProblem(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxComposeUploadBytes)
		if err := r.ParseMultipartForm(maxComposeUploadBytes); err != nil {
			writeComposeProblem(w, "上传内容无效或超过 3 MiB。")
			return
		}
		composeYAML, err := readUploadedFile(r, "compose", compose.MaxComposeBytes)
		if err != nil {
			writeComposeProblem(w, "compose.yaml 必须存在且不得超过 2 MiB。")
			return
		}
		environmentFile, err := readOptionalUploadedFile(r, "environment", compose.MaxEnvBytes)
		if err != nil {
			writeComposeProblem(w, ".env 不得超过 1 MiB。")
			return
		}
		environmentIDText := strings.TrimSpace(r.FormValue("environmentId"))
		if environmentIDText != "" {
			environmentID, parseErr := uuid.Parse(environmentIDText)
			if parseErr != nil || deps.Applications == nil {
				writeComposeProblem(w, "environmentId 必须指向已连接的 Docker Compose 源环境。")
				return
			}
			registered, registerErr := deps.Applications.RegisterCompose(r.Context(), environmentID, r.FormValue("projectName"), composeYAML, environmentFile)
			if registerErr != nil {
				writeApplicationError(w, registerErr)
				return
			}
			if deps.Audit != nil {
				serviceCount := 0
				if registered.Inventory.Compose != nil {
					serviceCount = len(registered.Inventory.Compose.Services)
				}
				auditApplication(deps, r, "compose.register", &registered.ID, "SUCCESS", map[string]any{"environmentId": environmentID, "projectName": registered.Name, "serviceCount": serviceCount})
			}
			writeJSON(w, http.StatusOK, registered)
			return
		}
		inventory, err := deps.Compose.Analyze(r.Context(), r.FormValue("projectName"), composeYAML, environmentFile)
		if err != nil {
			if errors.Is(err, compose.ErrInvalidCompose) {
				writeComposeProblem(w, strings.TrimPrefix(err.Error(), compose.ErrInvalidCompose.Error()+": "))
				return
			}
			writeInternalProblem(w)
			return
		}
		if deps.Audit != nil {
			actor := "unknown"
			if principal, ok := principalFromContext(r.Context()); ok {
				actor = principal.Administrator.Username
			}
			_ = deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: "compose.analyze", ObjectType: "compose", Result: "SUCCESS", Detail: map[string]any{
				"projectName": inventory.ProjectName, "serviceCount": len(inventory.Services), "volumeCount": len(inventory.Volumes),
			}})
		}
		writeJSON(w, http.StatusOK, inventory)
	}
}

func readUploadedFile(r *http.Request, name string, limit int64) ([]byte, error) {
	file, _, err := r.FormFile(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(file, limit)
}

func readOptionalUploadedFile(r *http.Request, name string, limit int64) ([]byte, error) {
	file, _, err := r.FormFile(name)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(file, limit)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(value)) > limit {
		return nil, errors.New("file exceeds limit")
	}
	return value, nil
}

func writeComposeProblem(w http.ResponseWriter, detail string) {
	writeProblem(w, problem{Type: "/problems/invalid-compose", Title: "Invalid Compose configuration", Status: http.StatusBadRequest, Detail: detail, Code: "COMPOSE_INVALID"})
}
