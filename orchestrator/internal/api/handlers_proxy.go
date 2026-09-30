package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

// ProxyStatusResponse represents the operational status of the proxy traffic bridge.
type ProxyStatusResponse struct {
	Status             string   `json:"status"`
	Mode               string   `json:"mode"`
	CAInstalled        bool     `json:"ca_installed"`
	SupportedProtocols []string `json:"supported_protocols"`
	SupportedSources   []string `json:"supported_sources"`
}

// HandleProxyStatus returns the health and configuration of the proxy bridge.
// GET /api/v1/proxy/status
func HandleProxyStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := ProxyStatusResponse{
			Status:             "ready",
			Mode:               "passive_ingest",
			CAInstalled:        true,
			SupportedProtocols: []string{"http/1.1", "http/2", "websocket"},
			SupportedSources:   []string{"caido", "burpsuite", "mitmproxy", "curl", "custom"},
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// ProxyInjectRequest represents an HTTP flow injected into the knowledge graph.
type ProxyInjectRequest struct {
	SessionID       string              `json:"session_id"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	Headers         map[string][]string `json:"headers,omitempty"`
	RequestBody     string              `json:"request_body,omitempty"`
	StatusCode      int                 `json:"status_code,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	ResponseBody    string              `json:"response_body,omitempty"`
	Tags            []string            `json:"tags,omitempty"`
}

// ProxyInjectResponse summarizes the results of proxy traffic ingestion.
type ProxyInjectResponse struct {
	Status        string   `json:"status"`
	AssetsCreated int      `json:"assets_created"`
	Host          string   `json:"host"`
	Endpoint      string   `json:"endpoint"`
	Parameters    []string `json:"parameters,omitempty"`
}

// HandleProxyInject ingests external proxy flows (Caido/Burp/mitmproxy) into the ASM knowledge store.
// POST /api/v1/proxy/inject
func HandleProxyInject(ks *knowledge.Store, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ProxyInjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body json", err)
			return
		}

		rawURL := strings.TrimSpace(req.URL)
		if rawURL == "" {
			writeError(w, http.StatusBadRequest, "url is required", nil)
			return
		}

		sessionID := req.SessionID
		if sessionID != "" && sm != nil {
			if _, err := sm.Get(sessionID); err != nil {
				writeError(w, http.StatusNotFound, "session not found", err)
				return
			}
		}

		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			writeError(w, http.StatusBadRequest, "invalid target url format", err)
			return
		}

		createdCount := 0
		host := parsed.Hostname()
		endpointPath := parsed.Path
		if endpointPath == "" {
			endpointPath = "/"
		}

		var params []string
		for k := range parsed.Query() {
			if k != "" {
				params = append(params, k)
			}
		}

		if ks != nil && sessionID != "" {
			// 1. Create Host / Subdomain Asset
			hostType := knowledge.AssetDomain
			if strings.Count(host, ".") > 1 && !strings.HasPrefix(host, "127.") && !strings.HasPrefix(host, "192.168.") && !strings.HasPrefix(host, "10.") {
				hostType = knowledge.AssetSubdomain
			}
			hostNode, err := ks.SaveAsset(r.Context(), knowledge.AssetNode{
				SessionID: sessionID,
				Type:      hostType,
				Value:     host,
				Metadata: map[string]any{
					"source": "proxy_bridge",
					"scheme": parsed.Scheme,
					"port":   parsed.Port(),
				},
			})
			if err == nil {
				createdCount++
			}

			// 2. Create Endpoint Asset
			parentID := ""
			if err == nil && hostNode.ID != "" {
				parentID = hostNode.ID
			}
			method := strings.ToUpper(req.Method)
			if method == "" {
				method = "GET"
			}
			endpointVal := method + " " + endpointPath
			epNode, err := ks.SaveAsset(r.Context(), knowledge.AssetNode{
				SessionID: sessionID,
				ParentID:  parentID,
				Type:      knowledge.AssetEndpoint,
				Value:     endpointVal,
				Metadata: map[string]any{
					"method":      method,
					"path":        endpointPath,
					"status_code": req.StatusCode,
					"tags":        req.Tags,
				},
			})
			if err == nil {
				createdCount++
			}

			// 3. Create Parameter Assets
			epParentID := ""
			if err == nil && epNode.ID != "" {
				epParentID = epNode.ID
			}
			for _, p := range params {
				_, err := ks.SaveAsset(r.Context(), knowledge.AssetNode{
					SessionID: sessionID,
					ParentID:  epParentID,
					Type:      knowledge.AssetParameter,
					Value:     p,
					Metadata: map[string]any{
						"location": "query",
					},
				})
				if err == nil {
					createdCount++
				}
			}
		}

		resp := ProxyInjectResponse{
			Status:        "ingested",
			AssetsCreated: createdCount,
			Host:          host,
			Endpoint:      endpointPath,
			Parameters:    params,
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}
