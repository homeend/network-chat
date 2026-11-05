package api

import (
	"errors"
	"net/http"

	"netchat/internal/httpx"
	"netchat/internal/skills"
)

// Agent skills: GET /v1/skills, POST /v1/skills/{agent} (install/update),
// DELETE /v1/skills/{agent}.
func registerSkills(mux *http.ServeMux) {
	m, err := skills.NewManager()
	if err != nil {
		return
	}
	fail := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, skills.ErrUnknownAgent):
			httpx.Error(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, skills.ErrForeign):
			httpx.Error(w, http.StatusConflict, "foreign", err.Error())
		default:
			httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		}
	}
	mux.HandleFunc("GET /v1/skills", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, m.Overview())
	})
	mux.HandleFunc("POST /v1/skills/{agent}", func(w http.ResponseWriter, r *http.Request) {
		st, err := m.Install(r.PathValue("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("DELETE /v1/skills/{agent}", func(w http.ResponseWriter, r *http.Request) {
		st, err := m.Remove(r.PathValue("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, st)
	})
}
