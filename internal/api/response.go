package api

import (
	"encoding/json"
	"net/http"
)

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func jsonCreated(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func jsonError(w http.ResponseWriter, code int, errMsg, errCode, hint string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"error":         errMsg,
		"code":          errCode,
		"recovery_hint": hint,
	})
}

func jsonBadRequest(w http.ResponseWriter, msg string) {
	jsonError(w, http.StatusBadRequest, msg, "BAD_REQUEST", "Check request parameters")
}

func jsonNotFound(w http.ResponseWriter) {
	jsonError(w, http.StatusNotFound, "not found", "NOT_FOUND", "")
}

func jsonInternalError(w http.ResponseWriter, msg string) {
	jsonError(w, http.StatusInternalServerError, msg, "INTERNAL_ERROR", "Contact support if this persists")
}
