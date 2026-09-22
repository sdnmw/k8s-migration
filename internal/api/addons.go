package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/repository"
	veleroservice "github.com/smartx/sks-migration-center/internal/velero"
)

type VeleroAddonService interface {
	Install(context.Context, veleroservice.InstallInput) (veleroservice.InstallResult, error)
	Repair(context.Context, veleroservice.InstallInput) (veleroservice.InstallResult, error)
	Reuse(context.Context, veleroservice.ReuseInput) (veleroservice.InstallResult, error)
	Status(context.Context, uuid.UUID) (veleroservice.InstallResult, error)
	Uninstall(context.Context, uuid.UUID) (platform.AddonInstallation, error)
}

func veleroStatusHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Velero == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.PathValue("environmentId"))
		if err != nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		result, err := deps.Velero.Status(r.Context(), environmentID)
		if err != nil {
			writeAddonError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

type addonInstallRequest struct {
	Type                   string    `json:"type"`
	ObjectStorageProfileID uuid.UUID `json:"objectStorageProfileId"`
	Prefix                 string    `json:"prefix,omitempty"`
	KubeletRoot            string    `json:"kubeletRoot,omitempty"`
}

type addonReuseRequest struct {
	ObjectStorageProfileID uuid.UUID `json:"objectStorageProfileId"`
	Prefix                 string    `json:"prefix,omitempty"`
}

func reuseVeleroHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Velero == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.PathValue("environmentId"))
		if err != nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		var input addonReuseRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.ObjectStorageProfileID == uuid.Nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "objectStorageProfileId 必填。")
			return
		}
		result, err := deps.Velero.Reuse(r.Context(), veleroservice.ReuseInput{
			EnvironmentID: environmentID, ObjectStorageProfile: input.ObjectStorageProfileID, Prefix: input.Prefix,
		})
		if err != nil {
			writeAddonError(w, err)
			auditAddon(deps, r, "addon.velero.reuse", environmentID, "FAILURE", nil)
			return
		}
		auditAddon(deps, r, "addon.velero.reuse", environmentID, "SUCCESS", map[string]any{
			"version": result.Installation.Version, "backupStorageLocation": result.Location.Name,
		})
		writeJSON(w, http.StatusCreated, result)
	}
}

func uninstallAddonHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Velero == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.PathValue("environmentId"))
		if err != nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		value, err := deps.Velero.Uninstall(r.Context(), environmentID)
		if err != nil {
			writeAddonError(w, err)
			auditAddon(deps, r, "addon.velero.uninstall", environmentID, "FAILURE", nil)
			return
		}
		auditAddon(deps, r, "addon.velero.uninstall", environmentID, "SUCCESS", map[string]any{"status": value.Status})
		writeJSON(w, http.StatusOK, value)
	}
}

func installAddonHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Velero == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.PathValue("environmentId"))
		if err != nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		var input addonInstallRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Type != "VELERO" || input.ObjectStorageProfileID == uuid.Nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "当前仅支持 type=VELERO，且 objectStorageProfileId 必填。")
			return
		}
		result, err := deps.Velero.Install(r.Context(), veleroservice.InstallInput{
			EnvironmentID: environmentID, ObjectStorageProfile: input.ObjectStorageProfileID, Prefix: input.Prefix, KubeletRoot: input.KubeletRoot,
		})
		if err != nil {
			writeAddonError(w, err)
			auditAddon(deps, r, "addon.velero.install", environmentID, "FAILURE", nil)
			return
		}
		auditAddon(deps, r, "addon.velero.install", environmentID, "SUCCESS", map[string]any{
			"version": result.Installation.Version, "backupStorageLocation": result.Location.Name,
		})
		writeJSON(w, http.StatusCreated, result)
	}
}

func repairVeleroHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Velero == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.PathValue("environmentId"))
		if err != nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		var input addonInstallRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Type != "VELERO" || input.ObjectStorageProfileID == uuid.Nil {
			writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", "当前仅支持 type=VELERO，且 objectStorageProfileId 必填。")
			return
		}
		result, err := deps.Velero.Repair(r.Context(), veleroservice.InstallInput{
			EnvironmentID: environmentID, ObjectStorageProfile: input.ObjectStorageProfileID, Prefix: input.Prefix, KubeletRoot: input.KubeletRoot,
		})
		if err != nil {
			writeAddonError(w, err)
			auditAddon(deps, r, "addon.velero.repair", environmentID, "FAILURE", nil)
			return
		}
		auditAddon(deps, r, "addon.velero.repair", environmentID, "SUCCESS", map[string]any{
			"version": result.Installation.Version, "backupStorageLocation": result.Location.Name,
		})
		writeJSON(w, http.StatusOK, result)
	}
}

func writeAddonError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, veleroservice.ErrInvalidInput):
		writeAddonProblem(w, http.StatusBadRequest, "ADDON_INVALID", err.Error())
	case errors.Is(err, veleroservice.ErrUnmanagedInstallation):
		writeAddonProblem(w, http.StatusConflict, "VELERO_EXISTING_UNMANAGED", "集群已存在非本系统管理的 Velero；请先选择复用、迁移或卸载策略，系统未改动现有安装。")
	case errors.Is(err, repository.ErrNotFound):
		writeAddonProblem(w, http.StatusNotFound, "ADDON_DEPENDENCY_NOT_FOUND", "环境或对象存储配置不存在。")
	case errors.Is(err, repository.ErrConflict):
		writeAddonProblem(w, http.StatusConflict, "ADDON_CONFLICT", "Add-on 状态发生冲突。")
	default:
		writeInternalProblem(w)
	}
}

func writeAddonProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/addon", Title: "Add-on error", Status: status, Detail: detail, Code: code})
}

func auditAddon(deps Dependencies, r *http.Request, action string, environmentID uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{
		Actor: actor, Action: action, ObjectType: "addon", ObjectID: &environmentID, Result: result, Detail: detail,
	}); err != nil && deps.Logger != nil {
		deps.Logger.Error("add-on audit write failed", "action", action, "error", err)
	}
}
