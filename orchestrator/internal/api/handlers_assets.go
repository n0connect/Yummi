package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// HandleListAssets returns all assets or hierarchical asset trees for a session.
func HandleListAssets(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if sessionID == "" {
			sessionID = chi.URLParam(r, "id")
		}
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "Invalid session ID format", nil)
			return
		}

		if r.URL.Query().Get("format") == "tree" {
			tree, err := ks.GetAssetTree(r.Context(), sessionID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error(), err)
				return
			}
			if tree == nil {
				tree = make([]*knowledge.AssetTree, 0)
			}
			writeJSON(w, http.StatusOK, tree)
			return
		}

		assets, err := ks.ListAssets(r.Context(), sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), err)
			return
		}
		if assets == nil {
			assets = make([]knowledge.AssetNode, 0)
		}
		writeJSON(w, http.StatusOK, assets)
	}
}

// HandleCreateAsset records a new asset node.
func HandleCreateAsset(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if sessionID == "" {
			sessionID = chi.URLParam(r, "id")
		}
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "Invalid session ID format", nil)
			return
		}

		var req struct {
			ParentID string         `json:"parent_id,omitempty"`
			Type     string         `json:"type"`
			Value    string         `json:"value"`
			Metadata map[string]any `json:"metadata,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body JSON", err)
			return
		}

		val := strings.TrimSpace(req.Value)
		if val == "" {
			writeError(w, http.StatusBadRequest, "Asset value is required", nil)
			return
		}
		typ := strings.TrimSpace(req.Type)
		if typ == "" {
			typ = string(knowledge.AssetEndpoint)
		}

		node, err := ks.SaveAsset(r.Context(), knowledge.AssetNode{
			SessionID: sessionID,
			ParentID:  req.ParentID,
			Type:      knowledge.AssetType(typ),
			Value:     val,
			Metadata:  req.Metadata,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), err)
			return
		}

		writeJSON(w, http.StatusCreated, node)
	}
}
