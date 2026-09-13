package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/objectstorage"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type ObjectStorageService interface {
	Bootstrap(context.Context, objectstorage.BootstrapInput) (objectstorage.BootstrapResult, error)
	Adopt(context.Context, objectstorage.AdoptInput) (objectstorage.BootstrapResult, error)
	ConnectExternal(context.Context, objectstorage.ExternalInput) (objectstorage.ProfileResult, error)
	Test(context.Context, uuid.UUID) ([]objectstorage.Check, error)
	Profiles(context.Context) ([]platform.ObjectStorageProfile, error)
	Addons(context.Context, uuid.UUID) ([]platform.AddonInstallation, error)
	Policy() objectstorage.SourcePolicy
}

func connectExternalObjectStorageHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		var input objectstorage.ExternalInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeObjectStorageProblem(w, http.StatusBadRequest, "OBJECT_STORAGE_INVALID", "外部 S3 请求格式无效。")
			return
		}
		result, err := deps.ObjectStorage.ConnectExternal(r.Context(), input)
		if err != nil {
			writeObjectStorageError(w, err)
			auditObjectStorage(deps, r, "object_storage.external.connect", nil, "FAILURE", map[string]any{"name": strings.TrimSpace(input.Name)})
			return
		}
		auditObjectStorage(deps, r, "object_storage.external.connect", &result.Profile.ID, "SUCCESS", map[string]any{"name": result.Profile.Name, "bucket": result.Profile.Bucket})
		writeJSON(w, http.StatusCreated, result)
	}
}

func adoptObjectStorageHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		var input objectstorage.AdoptInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeObjectStorageProblem(w, http.StatusBadRequest, "OBJECT_STORAGE_INVALID", "MinIO 接管请求格式无效。")
			return
		}
		result, err := deps.ObjectStorage.Adopt(r.Context(), input)
		if err != nil {
			writeObjectStorageError(w, err)
			auditObjectStorage(deps, r, "object_storage.adopt", &input.EnvironmentID, "FAILURE", nil)
			return
		}
		auditObjectStorage(deps, r, "object_storage.adopt", &input.EnvironmentID, "SUCCESS", map[string]any{"profileId": result.Profile.ID, "bucket": result.Profile.Bucket})
		writeJSON(w, http.StatusOK, result)
	}
}

func listObjectStorageProfilesHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		values, err := deps.ObjectStorage.Profiles(r.Context())
		if err != nil {
			writeObjectStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func bootstrapObjectStorageHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		var input objectstorage.BootstrapInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeObjectStorageProblem(w, http.StatusBadRequest, "OBJECT_STORAGE_INVALID", "MinIO 引导请求格式无效。")
			return
		}
		result, err := deps.ObjectStorage.Bootstrap(r.Context(), input)
		if err != nil {
			writeObjectStorageError(w, err)
			auditObjectStorage(deps, r, "object_storage.bootstrap", &input.EnvironmentID, "FAILURE", map[string]any{"storageClass": input.StorageClass})
			return
		}
		auditObjectStorage(deps, r, "object_storage.bootstrap", &input.EnvironmentID, "SUCCESS", map[string]any{"profileId": result.Profile.ID, "bucket": result.Profile.Bucket})
		writeJSON(w, http.StatusCreated, result)
	}
}

func testObjectStorageHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		var input struct {
			ProfileID uuid.UUID `json:"profileId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.ProfileID == uuid.Nil {
			writeObjectStorageProblem(w, http.StatusBadRequest, "OBJECT_STORAGE_INVALID", "profileId 必须是有效的 UUID。")
			return
		}
		checks, err := deps.ObjectStorage.Test(r.Context(), input.ProfileID)
		if err != nil {
			writeObjectStorageError(w, err)
			auditObjectStorage(deps, r, "object_storage.test", &input.ProfileID, "FAILURE", nil)
			return
		}
		auditObjectStorage(deps, r, "object_storage.test", &input.ProfileID, "SUCCESS", nil)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "checks": checks})
	}
}

func listAddonStatusHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.PathValue("environmentId"))
		if err != nil {
			writeObjectStorageProblem(w, http.StatusBadRequest, "OBJECT_STORAGE_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		values, err := deps.ObjectStorage.Addons(r.Context(), environmentID)
		if err != nil {
			writeObjectStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func getMinIOSourcePolicyHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if deps.ObjectStorage == nil {
			writeInternalProblem(w)
			return
		}
		writeJSON(w, http.StatusOK, deps.ObjectStorage.Policy())
	}
}

func writeObjectStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, objectstorage.ErrReleaseBlocked):
		writeObjectStorageProblem(w, http.StatusUnprocessableEntity, "MINIO_RELEASE_BLOCKED", err.Error())
	case errors.Is(err, objectstorage.ErrInvalidInput):
		writeObjectStorageProblem(w, http.StatusBadRequest, "OBJECT_STORAGE_INVALID", strings.TrimPrefix(err.Error(), objectstorage.ErrInvalidInput.Error()+": "))
	case errors.Is(err, objectstorage.ErrConflict), errors.Is(err, repository.ErrConflict):
		writeObjectStorageProblem(w, http.StatusConflict, "OBJECT_STORAGE_CONFLICT", "目标集群已存在受管 MinIO，或对象存储名称发生冲突。")
	case errors.Is(err, repository.ErrNotFound):
		writeObjectStorageProblem(w, http.StatusNotFound, "OBJECT_STORAGE_NOT_FOUND", "目标环境或对象存储配置不存在。")
	default:
		writeInternalProblem(w)
	}
}

func writeObjectStorageProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/object-storage", Title: "Object storage error", Status: status, Detail: detail, Code: code})
}

func auditObjectStorage(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "object_storage", ObjectID: objectID, Result: result, Detail: detail}); err != nil && deps.Logger != nil {
		deps.Logger.Error("object storage audit write failed", "action", action, "error", err)
	}
}
