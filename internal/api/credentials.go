package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type createCredentialRequest struct {
	Name    string                `json:"name"`
	Type    domaincredential.Type `json:"type"`
	Payload string                `json:"payload"`
}

func listCredentialsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Credentials == nil {
			writeInternalProblem(w)
			return
		}
		values, err := deps.Credentials.List(r.Context())
		if err != nil {
			writeInternalProblem(w)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

func createCredentialHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Credentials == nil {
			writeInternalProblem(w)
			return
		}
		var request createCredentialRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || strings.TrimSpace(request.Name) == "" || request.Payload == "" || !request.Type.Valid() || request.Type == domaincredential.TypeCompose {
			writeProblem(w, problem{Type: "/problems/credential", Title: "Credential error", Status: http.StatusBadRequest, Detail: "名称、凭证类型和凭证内容不能为空。", Code: "CREDENTIAL_INVALID"})
			return
		}
		value, err := deps.Credentials.Store(r.Context(), strings.TrimSpace(request.Name), request.Type, []byte(request.Payload))
		if err != nil {
			writeInternalProblem(w)
			return
		}
		writeJSON(w, http.StatusCreated, value)
	}
}

func deleteCredentialHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("credentialId"))
		if err != nil {
			writeProblem(w, problem{Type: "/problems/credential", Title: "Credential error", Status: http.StatusBadRequest, Detail: "credentialId 必须是有效 UUID。", Code: "CREDENTIAL_INVALID"})
			return
		}
		if deps.Credentials == nil {
			writeInternalProblem(w)
			return
		}
		if err := deps.Credentials.Delete(r.Context(), id); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				writeProblem(w, problem{Type: "/problems/credential", Title: "Credential error", Status: http.StatusNotFound, Detail: "凭证不存在。", Code: "CREDENTIAL_NOT_FOUND"})
				return
			}
			writeInternalProblem(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
