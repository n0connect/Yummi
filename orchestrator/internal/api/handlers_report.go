package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/report"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

// HandleGenerateReport exports session targets, findings, and asset maps in various formats.
// GET /api/v1/sessions/{sessionID}/report?format=hackerone|bugcrowd|executive|json
func HandleGenerateReport(ks *knowledge.Store, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if sessionID == "" {
			sessionID = chi.URLParam(r, "id")
		}
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}

		if sm != nil {
			if _, err := sm.Get(sessionID); err != nil {
				writeError(w, http.StatusNotFound, "session not found", err)
				return
			}
		}

		fmtParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
		if fmtParam == "" {
			fmtParam = string(report.FormatHackerOne)
		}

		repFmt := report.ReportFormat(fmtParam)
		switch repFmt {
		case report.FormatHackerOne, report.FormatBugcrowd, report.FormatExecutive, report.FormatJSON:
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported format '%s' (allowed: hackerone, bugcrowd, executive, json)", fmtParam), nil)
			return
		}

		var targets []knowledge.Target
		var findings []knowledge.Finding
		var assets []knowledge.AssetNode
		var scopeRules []knowledge.ScopeRule

		if ks != nil {
			targets = ks.ListTargets(sessionID)
			findings = ks.ListFindings(sessionID)
			var err error
			assets, err = ks.ListAssets(r.Context(), sessionID)
			if err != nil {
				// Non-fatal, log or continue with empty assets
				assets = nil
			}
			scopeRules, err = ks.ListScopeRules(r.Context(), sessionID)
			if err != nil {
				// Non-fatal — report still generates without scope classification.
				scopeRules = nil
			}
		}

		data := report.ReportData{
			SessionID:   sessionID,
			GeneratedAt: time.Now().UTC(),
			Targets:     targets,
			Findings:    findings,
			Assets:      assets,
			ScopeRules:  scopeRules,
		}

		out, contentType, err := report.Generate(data, repFmt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate report", err)
			return
		}

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(out))
	}
}
