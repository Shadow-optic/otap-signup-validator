// Schema REST API handlers. Mounted alongside the grpc-gateway routes.
//
// The existing FLR uses protoc-generated gRPC + grpc-gateway. To avoid
// requiring a protoc rebuild step in this code drop, schema endpoints are
// implemented as direct http.HandlerFunc bindings on the same http.Server.
// They reach the same registry.Engine instance via *Server.registry.
//
// Routes:
//   POST   /v1/schemas                            create
//   GET    /v1/schemas/{id}                       get by id
//   GET    /v1/schemas                            list with query-string filter
//   POST   /v1/schemas/{id}/deprecate             mark deprecated
//   POST   /v1/schemas/{id}/revoke                mark revoked
//   GET    /v1/schemas/active/{operator}/{oam}    get active for (op, oam)
//   GET    /v1/schemas/commitment                 build current schema commitment

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
)

// RegisterSchemaRoutes wires the schema REST routes into a mux.
// Call this from the server's HTTP setup after the gateway mux is built.
func (s *Server) RegisterSchemaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/schemas", s.handleSchemas)
	mux.HandleFunc("/v1/schemas/", s.handleSchemaByPath)
}

// handleSchemas dispatches collection-level operations.
func (s *Server) handleSchemas(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createSchema(w, r)
	case http.MethodGet:
		s.listSchemas(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSchemaByPath dispatches per-resource operations.
func (s *Server) handleSchemaByPath(w http.ResponseWriter, r *http.Request) {
	// Trim /v1/schemas/ prefix
	rest := strings.TrimPrefix(r.URL.Path, "/v1/schemas/")
	parts := strings.Split(rest, "/")

	switch {
	case len(parts) == 1 && parts[0] != "":
		// /v1/schemas/{id}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.getSchema(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "deprecate":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.deprecateSchema(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "revoke":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.revokeSchema(w, r, parts[0])
	case len(parts) == 3 && parts[0] == "active":
		// /v1/schemas/active/{operator}/{oam}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		oam, err := strconv.ParseInt(parts[2], 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid oam mode")
			return
		}
		s.getActiveSchemaForOam(w, r, parts[1], int32(oam))
	case len(parts) == 1 && parts[0] == "commitment":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.getSchemaCommitment(w, r)
	default:
		writeError(w, http.StatusNotFound, "route not found")
	}
}

// --- handler implementations ---

func (s *Server) createSchema(w http.ResponseWriter, r *http.Request) {
	var sch models.ApplicationSchema
	if err := json.NewDecoder(r.Body).Decode(&sch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	out, err := s.registry.RegisterSchema(&sch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) getSchema(w http.ResponseWriter, _ *http.Request, id string) {
	sch, err := s.registry.GetSchema(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

func (s *Server) listSchemas(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := registry.SchemaFilter{
		OperatorID: q.Get("operator_id"),
		NameLike:   q.Get("name_like"),
	}
	if v := q.Get("status"); v != "" {
		filter.Status = parseSchemaStatus(v)
	}
	if v := q.Get("oam_mode"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid oam_mode")
			return
		}
		x := int32(n)
		filter.OamMode = &x
	}
	out, err := s.registry.ListSchemas(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Schemas []*models.ApplicationSchema `json:"schemas"`
	}{Schemas: out})
}

func (s *Server) deprecateSchema(w http.ResponseWriter, _ *http.Request, id string) {
	out, err := s.registry.DeprecateSchema(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) revokeSchema(w http.ResponseWriter, _ *http.Request, id string) {
	out, err := s.registry.RevokeSchema(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getActiveSchemaForOam(w http.ResponseWriter, _ *http.Request, op string, oam int32) {
	sch, err := s.registry.GetActiveSchemaForOam(op, oam)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

func (s *Server) getSchemaCommitment(w http.ResponseWriter, _ *http.Request) {
	c, err := s.registry.BuildSchemaCommitment()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func parseSchemaStatus(v string) models.SchemaStatus {
	switch strings.ToUpper(v) {
	case "ACTIVE":
		return models.SchemaStatusActive
	case "DEPRECATED":
		return models.SchemaStatusDeprecated
	case "REVOKED":
		return models.SchemaStatusRevoked
	default:
		return models.SchemaStatusUnspecified
	}
}
