package api

import "net/http"

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Code     string       `json:"code"`
	TraceID  string       `json:"traceId,omitempty"`
	Errors   []fieldError `json:"errors,omitempty"`
}

func writeProblem(w http.ResponseWriter, value problem) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(value.Status)
	_ = encodeJSON(w, value)
}
