// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// apiError is the JSON error body: {"error":{"code","message","params"}}.
type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string, params map[string]any) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message, Params: params}})
}

// decodeJSON reads a JSON body strictly (unknown fields rejected).
// It writes the error response itself and returns false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request.too_large", "Request body too large", nil)
			return false
		}
		writeError(w, http.StatusBadRequest, "validation.json", "Malformed JSON body", nil)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "validation.json", "Trailing data after JSON body", nil)
		return false
	}
	return true
}

func isJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return ct == "application/json" || strings.HasPrefix(ct, "application/json;")
}
