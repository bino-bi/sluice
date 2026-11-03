// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/synth/build"
)

// SynthService starts and reports synthetic builds. A nil value makes
// the /admin/synthetic routes respond 501.
type SynthService interface {
	// Start starts a build of name. It returns the running build and
	// true when one runs already.
	Start(ctx context.Context, name, remoteIP string) (build.BuildView, bool, error)
	Status(name string) (build.StatusView, error)
}

type synthStartResponse struct {
	Name           string `json:"name"`
	BuildID        string `json:"build_id"`
	State          string `json:"state"`
	AlreadyRunning bool   `json:"already_running"`
}

func (s *Server) synthNotEnabled(w http.ResponseWriter) bool {
	if s.deps.Synth != nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"no synthetic data source loaded"}`))
	return true
}

// handleSynthBuild starts a build and answers 202 at once. A retry while
// the build runs gets the same build, so it is harmless.
func (s *Server) handleSynthBuild(w http.ResponseWriter, r *http.Request) {
	if s.synthNotEnabled(w) {
		return
	}
	name := r.PathValue("name")
	v, running, err := s.deps.Synth.Start(r.Context(), name, identity.RemoteIP(r))
	if errors.Is(err, build.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		s.writeAPIError(w, r, err)
		return
	}
	w.Header().Set("Location", "/admin/synthetic/"+name)
	writeJSON(w, http.StatusAccepted, synthStartResponse{
		Name:           name,
		BuildID:        v.BuildID,
		State:          v.State,
		AlreadyRunning: running,
	})
}

// handleSynthStatus returns the state, the builds of this process and
// the report of the current build.
func (s *Server) handleSynthStatus(w http.ResponseWriter, r *http.Request) {
	if s.synthNotEnabled(w) {
		return
	}
	v, err := s.deps.Synth.Status(r.PathValue("name"))
	if errors.Is(err, build.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		s.writeAPIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
