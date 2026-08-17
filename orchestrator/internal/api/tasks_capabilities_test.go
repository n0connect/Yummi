package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

func TestHandleAgentCapabilitiesReportsConcreteBackendContract(t *testing.T) {
	runner := agent.NewRunner(nil, nil, nil, nil, agent.Config{})
	rec := httptest.NewRecorder()
	HandleAgentCapabilities(runner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var caps agent.BackendCapabilities
	if err := json.NewDecoder(rec.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	if caps.Backend != "graph" || !caps.Resumable || caps.ExecutionModel != "graph_state_machine" {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}
