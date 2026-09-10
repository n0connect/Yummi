package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AssetType categorizes the discovered asset in the Attack Surface Matrix.
type AssetType string

const (
	AssetDomain      AssetType = "domain"
	AssetSubdomain   AssetType = "subdomain"
	AssetIP          AssetType = "ip"
	AssetPort        AssetType = "port"
	AssetService     AssetType = "service"
	AssetEndpoint    AssetType = "endpoint"
	AssetParameter   AssetType = "parameter"
	AssetCloudBucket AssetType = "cloud_bucket"
	AssetSecret      AssetType = "secret"
)

// AssetNode represents a node in the hierarchical attack surface tree.
type AssetNode struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	ParentID  string         `json:"parent_id,omitempty"`
	Type      AssetType      `json:"type"`
	Value     string         `json:"value"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
}

// AssetTree is a tree representation of an attack surface for visualization.
type AssetTree struct {
	AssetNode
	Children []*AssetTree `json:"children,omitempty"`
}

// AssetDelta summarizes new vs existing assets.
type AssetDelta struct {
	TotalAssets int         `json:"total_assets"`
	NewAssets   []AssetNode `json:"new_assets,omitempty"`
}

const assetsSchema = `
CREATE TABLE IF NOT EXISTS assets (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    parent_id TEXT,
    type TEXT NOT NULL,
    value TEXT NOT NULL,
    metadata TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_k_assets_session ON assets(session_id);
CREATE INDEX IF NOT EXISTS idx_k_assets_parent ON assets(parent_id);
`

// SaveAsset upserts an asset record for a session.
func (s *Store) SaveAsset(ctx context.Context, a AssetNode) (AssetNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	if a.ID == "" {
		a.ID = uuid.New().String()
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	if a.Metadata == nil {
		a.Metadata = make(map[string]any)
	}

	if s.db != nil {
		metaBytes, err := json.Marshal(a.Metadata)
		if err != nil {
			return AssetNode{}, fmt.Errorf("marshal asset metadata: %w", err)
		}
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO assets (id, session_id, parent_id, type, value, metadata, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				parent_id=excluded.parent_id,
				type=excluded.type,
				value=excluded.value,
				metadata=excluded.metadata,
				updated_at=excluded.updated_at
		`, a.ID, a.SessionID, a.ParentID, string(a.Type), a.Value, string(metaBytes), a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return AssetNode{}, fmt.Errorf("insert/update asset: %w", err)
		}
	}

	if s.assets == nil {
		s.assets = make(map[string]AssetNode)
	}
	s.assets[a.ID] = a
	return a, nil
}

// ListAssets returns all assets for a given session.
func (s *Store) ListAssets(ctx context.Context, sessionID string) ([]AssetNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		var out []AssetNode
		for _, a := range s.assets {
			if a.SessionID == sessionID {
				out = append(out, a)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
		return out, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, parent_id, type, value, metadata, created_at, updated_at
		FROM assets
		WHERE session_id = ?
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AssetNode
	for rows.Next() {
		var a AssetNode
		var parentID, metaStr sql.NullString
		var typ string
		if err := rows.Scan(&a.ID, &a.SessionID, &parentID, &typ, &a.Value, &metaStr, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		a.Type = AssetType(typ)
		if parentID.Valid {
			a.ParentID = parentID.String
		}
		if metaStr.Valid && strings.TrimSpace(metaStr.String) != "" {
			_ = json.Unmarshal([]byte(metaStr.String), &a.Metadata)
		}
		if a.Metadata == nil {
			a.Metadata = make(map[string]any)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAssetTree builds a hierarchical tree of assets for a session.
func (s *Store) GetAssetTree(ctx context.Context, sessionID string) ([]*AssetTree, error) {
	nodes, err := s.ListAssets(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	nodeMap := make(map[string]*AssetTree, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID] = &AssetTree{
			AssetNode: n,
			Children:  make([]*AssetTree, 0),
		}
	}

	var roots []*AssetTree
	for _, n := range nodes {
		treeNode := nodeMap[n.ID]
		if n.ParentID != "" && nodeMap[n.ParentID] != nil {
			parent := nodeMap[n.ParentID]
			parent.Children = append(parent.Children, treeNode)
		} else {
			roots = append(roots, treeNode)
		}
	}

	return roots, nil
}
