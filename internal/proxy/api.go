package proxy

import (
	"net/http"
)

// handleAPI answers /api/ routes. Filled in by the API endpoints task.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, _ route) {
	http.NotFound(w, r)
}
