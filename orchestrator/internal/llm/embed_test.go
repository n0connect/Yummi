package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("expected /api/embeddings, got %s", r.URL.Path)
		}

		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "nomic-embed-text" {
			t.Errorf("expected model nomic-embed-text, got %q", req.Model)
		}
		if req.Prompt != "nmap -sV <TARGET>" {
			t.Errorf("expected prompt to be forwarded, got %q", req.Prompt)
		}

		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embedding: []float32{0.1, 0.2, 0.3}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model").WithEmbedModel("nomic-embed-text")
	vec, err := client.Embed(context.Background(), "nmap -sV <TARGET>")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3-dim vector, got %d", len(vec))
	}
}

func TestClientEmbedNoModelConfigured(t *testing.T) {
	client := NewClient("http://localhost:1", "test-model") // no WithEmbedModel call
	_, err := client.Embed(context.Background(), "some text")
	if err == nil {
		t.Fatal("expected an error when no embedding model is configured")
	}
	if !strings.Contains(err.Error(), "embedding model") {
		t.Fatalf("expected 'embedding model' in error, got: %v", err)
	}
}

func TestClientEmbedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model").WithEmbedModel("nomic-embed-text")
	_, err := client.Embed(context.Background(), "text")
	if err == nil {
		t.Fatal("expected an error on non-200 response")
	}
}

func TestClientEmbedEmptyVectorIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embedding: nil})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model").WithEmbedModel("nomic-embed-text")
	_, err := client.Embed(context.Background(), "text")
	if err == nil {
		t.Fatal("expected an error for an empty embedding vector")
	}
}
