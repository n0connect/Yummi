package knowledge

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestStore_Assets_MapBacked(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	root, err := s.SaveAsset(ctx, AssetNode{
		SessionID: "s1",
		Type:      AssetDomain,
		Value:     "example.com",
		Metadata:  map[string]any{"org": "Example Corp"},
	})
	if err != nil {
		t.Fatalf("SaveAsset root: %v", err)
	}

	sub, err := s.SaveAsset(ctx, AssetNode{
		SessionID: "s1",
		ParentID:  root.ID,
		Type:      AssetSubdomain,
		Value:     "api.example.com",
	})
	if err != nil {
		t.Fatalf("SaveAsset sub: %v", err)
	}

	endpoint, err := s.SaveAsset(ctx, AssetNode{
		SessionID: "s1",
		ParentID:  sub.ID,
		Type:      AssetEndpoint,
		Value:     "/v1/users",
		Metadata:  map[string]any{"method": "GET", "status": 200},
	})
	if err != nil {
		t.Fatalf("SaveAsset endpoint: %v", err)
	}

	assets, err := s.ListAssets(ctx, "s1")
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) != 3 {
		t.Fatalf("expected 3 assets, got %d", len(assets))
	}

	tree, err := s.GetAssetTree(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAssetTree: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 root tree node, got %d", len(tree))
	}
	if tree[0].Value != "example.com" {
		t.Errorf("root node value = %q, want 'example.com'", tree[0].Value)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].Value != "api.example.com" {
		t.Fatalf("expected 1 child with api.example.com")
	}
	if len(tree[0].Children[0].Children) != 1 || tree[0].Children[0].Children[0].Value != "/v1/users" {
		t.Fatalf("expected grandchild with /v1/users, got: %+v", tree[0].Children[0].Children)
	}
	_ = endpoint
}

func TestStore_Assets_SQLite(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	root, err := s.SaveAsset(ctx, AssetNode{
		SessionID: "sess_db",
		Type:      AssetDomain,
		Value:     "target.org",
		Metadata:  map[string]any{"tech": []any{"cloudflare", "nginx"}},
	})
	if err != nil {
		t.Fatalf("SaveAsset: %v", err)
	}

	list, err := s.ListAssets(ctx, "sess_db")
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(list) != 1 || list[0].Value != "target.org" {
		t.Fatalf("unexpected list output: %+v", list)
	}
	_ = root
}
