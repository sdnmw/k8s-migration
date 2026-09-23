package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	sshadapter "github.com/smartx/sks-migration-center/internal/adapter/ssh"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	environmentservice "github.com/smartx/sks-migration-center/internal/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type EnvironmentService interface {
	Create(context.Context, environmentservice.CreateInput) (domainenvironment.Environment, error)
	Get(context.Context, uuid.UUID) (domainenvironment.Environment, error)
	List(context.Context) ([]domainenvironment.Environment, error)
	Delete(context.Context, uuid.UUID) error
	TestConnection(context.Context, uuid.UUID) (domainenvironment.ConnectionTest, error)
	Capabilities(context.Context, uuid.UUID) (domainenvironment.Capabilities, error)
	RefreshCapabilities(context.Context, uuid.UUID) (domainenvironment.Capabilities, error)
	Namespaces(context.Context, uuid.UUID) ([]string, error)
}

type createEnvironmentRequest struct {
	Name       string                 `json:"name"`
	Role       domainenvironment.Role `json:"role"`
	Kind       domainenvironment.Kind `json:"kind"`
	Credential string                 `json:"credential,omitempty"`
	Endpoint   string                 `json:"endpoint,omitempty"`
	SSH        *sshadapter.Credential `json:"ssh,omitempty"`
}

func listEnvironmentsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		values, err := deps.Environments.List(r.Context())
		if err != nil {
			writeInternalProblem(w)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func createEnvironmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		var request createEnvironmentRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, environmentservice.MaxCredentialBytes+16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeInvalidEnvironmentProblem(w, "请求必须是有效的 JSON，且 kubeconfig 不得超过 1 MiB。")
			auditEnvironment(deps, r, "environment.create", nil, "FAILURE", map[string]any{"reason": "invalid request"})
			return
		}
		value, err := deps.Environments.Create(r.Context(), environmentservice.CreateInput{
			Name: request.Name, Role: request.Role, Kind: request.Kind, Credential: []byte(request.Credential),
			Endpoint: request.Endpoint, SSH: request.SSH,
		})
		if err != nil {
			writeEnvironmentError(w, err)
			auditEnvironment(deps, r, "environment.create", nil, "FAILURE", map[string]any{
				"name": strings.TrimSpace(request.Name), "role": request.Role, "kind": request.Kind,
			})
			return
		}
		auditEnvironment(deps, r, "environment.create", &value.ID, "SUCCESS", map[string]any{
			"name": value.Name, "role": value.Role, "kind": value.Kind, "endpoint": value.Endpoint,
		})
		writeJSON(w, http.StatusCreated, value)
	}
}

func getEnvironmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := environmentID(w, r)
		if !ok {
			return
		}
		value, err := deps.Environments.Get(r.Context(), id)
		if err != nil {
			writeEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func deleteEnvironmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := environmentID(w, r)
		if !ok {
			return
		}
		if err := deps.Environments.Delete(r.Context(), id); err != nil {
			writeDeleteEnvironmentError(w, err)
			auditEnvironment(deps, r, "environment.delete", &id, "FAILURE", nil)
			return
		}
		auditEnvironment(deps, r, "environment.delete", &id, "SUCCESS", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeDeleteEnvironmentError(w http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrConflict) {
		writeProblem(w, problem{
			Type:   "/problems/environment-in-use",
			Title:  "Environment is in use",
			Status: http.StatusConflict,
			Detail: "该环境仍被迁移计划引用，不能删除。修复 Velero 等迁移组件不需要删除环境；确需删除时，请先删除相关迁移任务和计划。",
			Code:   "ENVIRONMENT_IN_USE",
		})
		return
	}
	writeEnvironmentError(w, err)
}

func testEnvironmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := environmentID(w, r)
		if !ok {
			return
		}
		result, err := deps.Environments.TestConnection(r.Context(), id)
		if err != nil {
			writeEnvironmentError(w, err)
			auditEnvironment(deps, r, "environment.test", &id, "FAILURE", nil)
			return
		}
		auditResult := "SUCCESS"
		if !result.Success {
			auditResult = "FAILURE"
		}
		auditEnvironment(deps, r, "environment.test", &id, auditResult, map[string]any{"success": result.Success})
		writeJSON(w, http.StatusOK, result)
	}
}

func getEnvironmentCapabilitiesHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := environmentID(w, r)
		if !ok {
			return
		}
		capabilities, err := deps.Environments.Capabilities(r.Context(), id)
		if err != nil {
			writeEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, capabilities)
	}
}

func refreshEnvironmentCapabilitiesHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := environmentID(w, r)
		if !ok {
			return
		}
		capabilities, err := deps.Environments.RefreshCapabilities(r.Context(), id)
		if err != nil {
			writeEnvironmentError(w, err)
			auditEnvironment(deps, r, "environment.capabilities.refresh", &id, "FAILURE", nil)
			return
		}
		auditEnvironment(deps, r, "environment.capabilities.refresh", &id, "SUCCESS", map[string]any{"kubernetesVersion": capabilities.KubernetesVersion})
		writeJSON(w, http.StatusOK, capabilities)
	}
}

func listEnvironmentNamespacesHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Environments == nil {
			writeInternalProblem(w)
			return
		}
		id, ok := environmentID(w, r)
		if !ok {
			return
		}
		namespaces, err := deps.Environments.Namespaces(r.Context(), id)
		if err != nil {
			writeEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, namespaces)
	}
}

func environmentID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("environmentId"))
	if err != nil {
		writeInvalidEnvironmentProblem(w, "environmentId 必须是有效的 UUID。")
		return uuid.Nil, false
	}
	return id, true
}

func writeEnvironmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeProblem(w, problem{Type: "/problems/environment-not-found", Title: "Environment not found", Status: http.StatusNotFound, Detail: "指定的环境不存在。", Code: "ENVIRONMENT_NOT_FOUND"})
	case errors.Is(err, repository.ErrConflict):
		writeProblem(w, problem{Type: "/problems/environment-conflict", Title: "Environment already exists", Status: http.StatusConflict, Detail: "环境名称已经存在。", Code: "ENVIRONMENT_CONFLICT"})
	case errors.Is(err, environmentservice.ErrInvalidInput):
		writeInvalidEnvironmentProblem(w, strings.TrimPrefix(err.Error(), environmentservice.ErrInvalidInput.Error()+": "))
	case errors.Is(err, environmentservice.ErrConnection):
		writeProblem(w, problem{Type: "/problems/environment-connection", Title: "Environment connection failed", Status: http.StatusUnprocessableEntity, Detail: "无法从环境读取所需信息。", Code: "ENVIRONMENT_CONNECTION_FAILED"})
	default:
		writeInternalProblem(w)
	}
}

func writeInvalidEnvironmentProblem(w http.ResponseWriter, detail string) {
	writeProblem(w, problem{Type: "/problems/invalid-environment", Title: "Invalid environment", Status: http.StatusBadRequest, Detail: detail, Code: "ENVIRONMENT_INVALID"})
}

func auditEnvironment(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if detail == nil {
		detail = map[string]any{}
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{
		Actor: actor, Action: action, ObjectType: "environment", ObjectID: objectID, Result: result, Detail: detail,
	}); err != nil && deps.Logger != nil {
		deps.Logger.Error("environment audit write failed", "action", action, "result", result, "error", err)
	}
}
