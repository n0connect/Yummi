package eino

import (
	"context"
	"testing"
)

func TestResearchToolSpecsDoNotExposeProviderAuthority(t *testing.T) {
	for _, spec := range []pentestToolSpec{webSearchSpec, webFetchSpec, browserSessionSpec} {
		tool := newPentestTool(spec, pentestToolDeps{})
		info, err := tool.Info(context.Background())
		if err != nil || info.Name != spec.Name {
			t.Fatalf("tool schema unavailable: %v", err)
		}
		for _, key := range []string{"api_key", "endpoint", "headers", "script", "cookie"} {
			if _, ok := spec.Params[key]; ok {
				t.Fatalf("research tool exposed provider authority: %s", key)
			}
		}
	}
}
