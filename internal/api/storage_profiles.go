package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/domain/storage"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/storageprofile"
)

type StorageProfileService interface {
	Create(context.Context, storageprofile.Input) (storage.Profile, error)
	List(context.Context, *uuid.UUID) ([]storage.Profile, error)
	Install(context.Context, uuid.UUID) (storageprofile.TestResult, error)
	Test(context.Context, uuid.UUID) (storageprofile.TestResult, error)
}

func listStorageProfilesHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StorageProfiles == nil {
			writeInternalProblem(w)
			return
		}
		var environmentID *uuid.UUID
		if raw := r.URL.Query().Get("environmentId"); raw != "" {
			parsed, err := uuid.Parse(raw)
			if err != nil {
				writeStorageProblem(w, http.StatusBadRequest, "STORAGE_PROFILE_INVALID", "environmentId 必须是有效的 UUID。")
				return
			}
			environmentID = &parsed
		}
		values, err := deps.StorageProfiles.List(r.Context(), environmentID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func createStorageProfileHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StorageProfiles == nil {
			writeInternalProblem(w)
			return
		}
		var input storageprofile.Input
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeStorageProblem(w, http.StatusBadRequest, "STORAGE_PROFILE_INVALID", "存储配置请求格式无效。")
			return
		}
		value, err := deps.StorageProfiles.Create(r.Context(), input)
		if err != nil {
			writeStorageError(w, err)
			auditStorage(deps, r, "storage_profile.create", &input.EnvironmentID, "FAILURE", nil)
			return
		}
		auditStorage(deps, r, "storage_profile.create", &value.ID, "SUCCESS", map[string]any{"type": value.Type, "storageClass": value.StorageClassName})
		writeJSON(w, http.StatusCreated, value)
	}
}

func installStorageProfileHandler(deps Dependencies) http.HandlerFunc {
	return storageProfileActionHandler(deps, "install")
}

func testStorageProfileHandler(deps Dependencies) http.HandlerFunc {
	return storageProfileActionHandler(deps, "test")
}

func storageProfileActionHandler(deps Dependencies, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StorageProfiles == nil {
			writeInternalProblem(w)
			return
		}
		profileID, err := uuid.Parse(r.PathValue("profileId"))
		if err != nil {
			writeStorageProblem(w, http.StatusBadRequest, "STORAGE_PROFILE_INVALID", "profileId 必须是有效的 UUID。")
			return
		}
		var result storageprofile.TestResult
		if action == "install" {
			result, err = deps.StorageProfiles.Install(r.Context(), profileID)
		} else {
			result, err = deps.StorageProfiles.Test(r.Context(), profileID)
		}
		if err != nil {
			writeStorageError(w, err)
			auditStorage(deps, r, "storage_profile."+action, &profileID, "FAILURE", nil)
			return
		}
		auditStorage(deps, r, "storage_profile."+action, &profileID, "SUCCESS", map[string]any{"storageClass": result.Profile.StorageClassName})
		writeJSON(w, http.StatusOK, result)
	}
}

func writeStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storageprofile.ErrInvalidInput):
		writeStorageProblem(w, http.StatusBadRequest, "STORAGE_PROFILE_INVALID", err.Error())
	case errors.Is(err, repository.ErrConflict):
		writeStorageProblem(w, http.StatusConflict, "STORAGE_PROFILE_CONFLICT", "存储配置名称或 StorageClass 已存在。")
	case errors.Is(err, repository.ErrNotFound):
		writeStorageProblem(w, http.StatusNotFound, "STORAGE_PROFILE_NOT_FOUND", "存储配置或环境不存在。")
	default:
		writeInternalProblem(w)
	}
}

func writeStorageProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/storage-profile", Title: "Storage profile error", Status: status, Detail: detail, Code: code})
}

func auditStorage(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "storage_profile", ObjectID: objectID, Result: result, Detail: detail}); err != nil && deps.Logger != nil {
		deps.Logger.Error("storage profile audit write failed", "action", action, "error", err)
	}
}
