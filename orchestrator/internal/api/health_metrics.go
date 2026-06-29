package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/health"
)

// HandleHealthMetrics serves GET /api/v1/system/health-metrics?window=5m|15m|1h
func HandleHealthMetrics(agg *health.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := windowToSamples(r.URL.Query().Get("window"))
		samples := agg.Samples(n)
		writeJSON(w, http.StatusOK, map[string]any{
			"samples":  samples,
			"window":   r.URL.Query().Get("window"),
			"interval": 5,
		})
	}
}

// windowToSamples converts a duration string ("5m", "15m", "1h") to a sample count.
func windowToSamples(w string) int {
	w = strings.TrimSpace(strings.ToLower(w))
	switch w {
	case "5m":
		return 60
	case "15m":
		return 180
	case "1h", "":
		return 720
	default:
		if n, err := strconv.Atoi(w); err == nil && n > 0 {
			return clampLimit(n)
		}
		return 720
	}
}
