// Package httpx has small JSON helpers shared by both HTTP servers.
package httpx

import (
	"encoding/json"
	"io"
	"net/http"

	"netchat/internal/protocol"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, code, msg string) {
	var e protocol.ErrorBody
	e.Error.Code, e.Error.Message = code, msg
	JSON(w, status, e)
}

// Decode reads a JSON body of at most limit bytes into v.
func Decode(r *http.Request, limit int64, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, limit)).Decode(v)
}
