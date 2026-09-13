package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/transform"
)

const maxTransformUploadBytes = transform.MaxManifestBytes + 64*1024

type TransformService interface {
	Preview(context.Context, uuid.UUID, []byte) (transform.Result, error)
}

func previewTransformHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Transforms == nil {
			writeInternalProblem(w)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxTransformUploadBytes)
		if err := r.ParseMultipartForm(maxTransformUploadBytes); err != nil {
			writeTransformProblem(w, http.StatusBadRequest, "TRANSFORM_INVALID", "上传内容无效或超过 10 MiB。")
			return
		}
		defer r.MultipartForm.RemoveAll()
		profileID, err := uuid.Parse(strings.TrimSpace(r.FormValue("profileId")))
		if err != nil {
			writeTransformProblem(w, http.StatusBadRequest, "TRANSFORM_INVALID", "profileId 必须是有效的 UUID。")
			return
		}
		manifests, err := readUploadedFile(r, "manifests", transform.MaxManifestBytes)
		if err != nil {
			writeTransformProblem(w, http.StatusBadRequest, "TRANSFORM_INVALID", "Kubernetes YAML 必须存在且不得超过 10 MiB。")
			return
		}
		result, err := deps.Transforms.Preview(r.Context(), profileID, manifests)
		if err != nil {
			writeTransformError(w, err)
			auditTransform(deps, r, profileID, "FAILURE", 0, 0)
			return
		}
		auditTransform(deps, r, profileID, "SUCCESS", len(result.Documents), result.Changed)
		writeJSON(w, http.StatusOK, result)
	}
}

func writeTransformError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeTransformProblem(w, http.StatusNotFound, "MAPPING_NOT_FOUND", "映射配置不存在。")
	case errors.Is(err, transform.ErrInvalidManifest), errors.Is(err, transform.ErrInvalidInput):
		detail := strings.TrimPrefix(err.Error(), transform.ErrInvalidManifest.Error()+": ")
		detail = strings.TrimPrefix(detail, transform.ErrInvalidInput.Error()+": ")
		writeTransformProblem(w, http.StatusBadRequest, "TRANSFORM_INVALID", detail)
	default:
		writeInternalProblem(w)
	}
}

func writeTransformProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/transform", Title: "Manifest transform error", Status: status, Detail: detail, Code: code})
}

func auditTransform(deps Dependencies, r *http.Request, profileID uuid.UUID, result string, documentCount, changed int) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{
		Actor: actor, Action: "transform.preview", ObjectType: "mapping_profile", ObjectID: &profileID, Result: result,
		Detail: map[string]any{"documentCount": documentCount, "changed": changed},
	}); err != nil && deps.Logger != nil {
		deps.Logger.Error("transform audit write failed", "error", err)
	}
}
