package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"

	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

// NewSandboxPolicyRouter exposes gateway decisions only. It deliberately has
// no operator routes, rule mutation, sessions, execution or approval endpoints.
// POST checks a literal IP; it does not grant access or resolve a hostname.
func NewSandboxPolicyRouter(pc *gateway.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hostname-policy", HandleGatewayHostnamePolicy(pc))
	mux.HandleFunc("POST /v1/egress/check", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var input struct {
			IP string `json:"ip"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "a literal IP is required", nil)
			return
		}
		if _, err := netip.ParseAddr(input.IP); err != nil || dec.Decode(new(any)) != io.EOF {
			writeError(w, http.StatusBadRequest, "a literal IP is required", nil)
			return
		}
		if pc == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway unavailable", nil)
			return
		}
		verdict, err := pc.CheckEgress(input.IP)
		if err != nil || (verdict != "allow" && verdict != "block" && verdict != "needs_approval") {
			writeError(w, http.StatusServiceUnavailable, "gateway unavailable", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"verdict": verdict})
	})
	return mux
}
