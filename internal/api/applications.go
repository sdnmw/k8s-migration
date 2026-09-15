package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	applicationservice "github.com/smartx/sks-migration-center/internal/application"
	domainapplication "github.com/smartx/sks-migration-center/internal/domain/application"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type ApplicationService interface {
	Discover(context.Context, uuid.UUID, string) (domainapplication.SourceApplication, error)
	DiscoverSelected(context.Context, uuid.UUID, string, string, []domainapplication.ResourceReference) (domainapplication.SourceApplication, error)
	DiscoverAll(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error)
	DiscoverCompose(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error)
	Preview(context.Context, uuid.UUID, string) (domainapplication.Inventory, error)
	RegisterCompose(context.Context, uuid.UUID, string, []byte, []byte) (domainapplication.SourceApplication, error)
	Get(context.Context, uuid.UUID) (domainapplication.SourceApplication, error)
	List(context.Context, uuid.UUID) ([]domainapplication.SourceApplication, error)
}

type discoverApplicationRequest struct {
	EnvironmentID uuid.UUID                             `json:"environmentId"`
	Namespace     string                                `json:"namespace"`
	Name          string                                `json:"name,omitempty"`
	Resources     []domainapplication.ResourceReference `json:"resources,omitempty"`
}

func discoverApplicationHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Applications == nil {
			writeInternalProblem(w)
			return
		}
		var request discoverApplicationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", "资源选择请求格式不正确或超出 2 MiB，请刷新页面重新读取资源后保存。")
			return
		}
		value, err := deps.Applications.DiscoverSelected(r.Context(), request.EnvironmentID, request.Namespace, request.Name, request.Resources)
		if err != nil {
			writeApplicationError(w, err)
			auditApplication(deps, r, "application.discover", nil, "FAILURE", map[string]any{"environmentId": request.EnvironmentID, "namespace": strings.TrimSpace(request.Namespace)})
			return
		}
		auditApplication(deps, r, "application.discover", &value.ID, "SUCCESS", map[string]any{"environmentId": value.EnvironmentID, "namespace": value.Namespace, "resourceCount": len(value.Inventory.Resources)})
		writeJSON(w, http.StatusOK, value)
	}
}

func previewApplicationHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request discoverApplicationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if deps.Applications == nil || decoder.Decode(&request) != nil {
			writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", "请求必须包含有效的 environmentId 和 namespace。")
			return
		}
		value, err := deps.Applications.Preview(r.Context(), request.EnvironmentID, request.Namespace)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

type discoverAllRequest struct {
	EnvironmentID uuid.UUID `json:"environmentId"`
}

func discoverAllApplicationsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request discoverAllRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if deps.Applications == nil || decoder.Decode(&request) != nil || request.EnvironmentID == uuid.Nil {
			writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", "请求必须包含有效的 environmentId。")
			return
		}
		values, err := deps.Applications.DiscoverAll(r.Context(), request.EnvironmentID)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func discoverComposeApplicationsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request discoverAllRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if deps.Applications == nil || decoder.Decode(&request) != nil || request.EnvironmentID == uuid.Nil {
			writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", "请求必须包含有效的 environmentId。")
			return
		}
		values, err := deps.Applications.DiscoverCompose(r.Context(), request.EnvironmentID)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func listApplicationsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Applications == nil {
			writeInternalProblem(w)
			return
		}
		environmentID, err := uuid.Parse(r.URL.Query().Get("environmentId"))
		if err != nil {
			writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", "environmentId 必须是有效的 UUID。")
			return
		}
		values, err := deps.Applications.List(r.Context(), environmentID)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func getApplicationHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Applications == nil {
			writeInternalProblem(w)
			return
		}
		id, err := uuid.Parse(r.PathValue("applicationId"))
		if err != nil {
			writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", "applicationId 必须是有效的 UUID。")
			return
		}
		value, err := deps.Applications.Get(r.Context(), id)
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
}

func writeApplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeApplicationProblem(w, http.StatusNotFound, "APPLICATION_NOT_FOUND", "指定的源环境或应用不存在。")
	case errors.Is(err, applicationservice.ErrInvalidInput):
		writeApplicationProblem(w, http.StatusBadRequest, "APPLICATION_INVALID", strings.TrimPrefix(err.Error(), applicationservice.ErrInvalidInput.Error()+": "))
	case errors.Is(err, applicationservice.ErrDiscovery):
		writeApplicationProblem(w, http.StatusUnprocessableEntity, "APPLICATION_DISCOVERY_FAILED", applicationDiscoveryDetail(err))
	default:
		writeInternalProblem(w)
	}
}

func writeApplicationProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, problem{Type: "/problems/application", Title: "Application inventory error", Status: status, Detail: detail, Code: code})
}

func applicationDiscoveryDetail(err error) string {
	message := err.Error()
	if strings.Contains(message, "COMPOSE_CONFIG_PERMISSION") {
		return "Compose 项目存在，但当前 SSH 用户没有读取配置或引用文件的权限。请检查 compose 文件及 env_file 的访问权限，或更换有权限的 SSH 凭证后重新发现。"
	}
	if strings.Contains(message, "env_file") || strings.Contains(message, "label_file") || strings.Contains(message, "server-local file") {
		return "Compose 引用了主机上的其他配置文件。请使用“从 Compose 主机发现应用”，系统会在源主机解析 include、env_file 和覆盖文件；单独上传 compose.yaml 仅适用于自包含配置。"
	}
	if strings.Contains(message, "Compose 项目解析失败") {
		return "主机上的 Compose 项目已找到，但没有项目能形成有效 Inventory。请检查项目配置及引用文件；单个异常项目不会再阻止其他正常项目被发现。"
	}
	if strings.Contains(message, "YAML cannot be parsed") {
		return "Compose 文件不是有效 YAML，请检查文件内容和缩进；请上传文件本身，不要上传路径文本或终端输出。"
	}
	return "无法完成应用发现。请先测试源环境连接；Compose 项目请确认原始配置及引用文件仍存在，Kubernetes 请确认所选 Namespace 可读取。"
}

func auditApplication(deps Dependencies, r *http.Request, action string, objectID *uuid.UUID, result string, detail map[string]any) {
	if deps.Audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Administrator.Username
	}
	if err := deps.Audit.RecordAudit(r.Context(), identity.AuditEvent{Actor: actor, Action: action, ObjectType: "source_application", ObjectID: objectID, Result: result, Detail: detail}); err != nil && deps.Logger != nil {
		deps.Logger.Error("application audit write failed", "action", action, "error", err)
	}
}
