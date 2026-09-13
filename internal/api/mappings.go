package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	domainmapping "github.com/smartx/sks-migration-center/internal/domain/mapping"
	mappingservice "github.com/smartx/sks-migration-center/internal/mapping"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type MappingService interface {
	Create(context.Context, domainmapping.Profile) (domainmapping.Profile, error)
	Update(context.Context, uuid.UUID, domainmapping.Profile) (domainmapping.Profile, error)
	Get(context.Context, uuid.UUID) (domainmapping.Profile, error)
	List(context.Context, *uuid.UUID) ([]domainmapping.Profile, error)
	Delete(context.Context, uuid.UUID) error
}

type mappingInput struct {
	Name                string                           `json:"name"`
	TargetEnvironmentID uuid.UUID                        `json:"targetEnvironmentId"`
	Storage             []domainmapping.KeyValue         `json:"storageMappings"`
	Namespaces          []domainmapping.KeyValue         `json:"namespaceMappings"`
	Ingress             []domainmapping.KeyValue         `json:"ingressMappings"`
	Registries          []domainmapping.KeyValue         `json:"registryMappings"`
	NFS                 []domainmapping.NFSMapping       `json:"nfsMappings"`
	NodeLabels          []domainmapping.NodeLabelMapping `json:"nodeLabelMappings"`
}

func (input mappingInput) profile() domainmapping.Profile {
	return domainmapping.Profile{Name: input.Name, TargetEnvironmentID: input.TargetEnvironmentID, Storage: input.Storage, Namespaces: input.Namespaces, Ingress: input.Ingress, Registries: input.Registries, NFS: input.NFS, NodeLabels: input.NodeLabels}
}

func listMappingsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Mappings == nil {
			writeInternalProblem(w)
			return
		}
		var targetID *uuid.UUID
		if raw := strings.TrimSpace(r.URL.Query().Get("targetEnvironmentId")); raw != "" {
			parsed, err := uuid.Parse(raw)
			if err != nil {
				writeMappingProblem(w, http.StatusBadRequest, "MAPPING_INVALID", "targetEnvironmentId 必须是有效的 UUID。")
				return
			}
			targetID = &parsed
		}
		values, err := deps.Mappings.List(r.Context(), targetID)
		if err != nil {
			writeMappingError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func createMappingHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Mappings == nil {
			writeInternalProblem(w)
			return
		}
		input, ok := decodeMappingInput(w, r)
		if !ok {
			return
		}
		value, err := deps.Mappings.Create(r.Context(), input.profile())
		if err != nil {
			writeMappingError(w, err)
			auditMapping(deps, r, "mapping.create", nil, "FAILURE", input.Name)
			return
		}
		auditMapping(deps, r, "mapping.create", &value.ID, "SUCCESS", value.Name)
		writeJSON(w, http.StatusCreated, value)
	}
}

func getMappingHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Mappings == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := mappingID(w, r)
		if !ok {
			return
		}
		value, err := deps.Mappings.Get(r.Context(), id)
		if err != nil {
			writeMappingError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func updateMappingHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Mappings == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := mappingID(w, r)
		if !ok {
			return
		}
		input, ok := decodeMappingInput(w, r)
		if !ok {
			return
		}
		value, err := deps.Mappings.Update(r.Context(), id, input.profile())
		if err != nil {
			writeMappingError(w, err)
			auditMapping(deps, r, "mapping.update", &id, "FAILURE", input.Name)
			return
		}
		auditMapping(deps, r, "mapping.update", &id, "SUCCESS", value.Name)
		writeJSON(w, http.StatusOK, value)
	}
}

func deleteMappingHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Mappings == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := mappingID(w, r)
		if !ok {
			return
		}
		if err := deps.Mappings.Delete(r.Context(), id); err != nil {
			writeMappingError(w, err)
			return
		}
		auditMapping(deps, r, "mapping.delete", &id, "SUCCESS", "")
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeMappingInput(w http.ResponseWriter, r *http.Request) (mappingInput, bool) {
	var input mappingInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeMappingProblem(w, http.StatusBadRequest, "MAPPING_INVALID", "映射配置格式无效。")
		return mappingInput{}, false
	}
	return input, true
}

func mappingID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("profileId"))
	if err != nil {
		writeMappingProblem(w, http.StatusBadRequest, "MAPPING_INVALID", "profileId 必须是有效的 UUID。")
		return uuid.Nil, false
	}
	return id, true
}

func writeMappingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeMappingProblem(w, http.StatusNotFound, "MAPPING_NOT_FOUND", "目标环境或映射配置不存在。")
	case errors.Is(err, repository.ErrConflict):
		writeMappingProblem(w, http.StatusConflict, "MAPPING_CONFLICT", "该目标集群已存在同名映射配置。")
	case errors.Is(err, mappingservice.ErrInvalidInput):
		writeMappingProblem(w, http.StatusBadRequest, "MAPPING_INVALID", strings.TrimPrefix(err.Error(), mappingservice.ErrInvalidInput.Error()+": "))
	default:
		writeInternalProblem(w)
	}
}

func writeMappingProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/mapping", Title: "Mapping profile error", Status: status, Detail: detail, Code: code})
}

func auditMapping(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result, name string) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "mapping_profile", ObjectID: objectID, Result: result, Detail: map[string]any{"name": name}}); err != nil && deps.Logger != nil {
		deps.Logger.Error("mapping audit write failed", "action", action, "error", err)
	}
}
