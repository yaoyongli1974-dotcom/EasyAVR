package cluster

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

func TestAcceptAndNodes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	cfg := config.ClusterConfig{Enabled: true, NodeID: "node-a", Name: "A", APIBase: "http://a:18000"}
	svc := NewService(cfg, db)
	svc.registerSelf()
	svc.Accept(model.ClusterNode{NodeID: "node-b", Name: "B", APIBase: "http://b:18000"})

	nodes, err := svc.Nodes()
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if !nodes[0].IsSelf {
		t.Fatalf("self node should sort first: %+v", nodes[0])
	}

	// A stale peer becomes offline.
	db.Model(&model.ClusterNode{}).Where("node_id = ?", "node-b").
		Update("last_heartbeat", time.Now().Add(-time.Hour))
	nodes, _ = svc.Nodes()
	for _, n := range nodes {
		if n.NodeID == "node-b" && n.Status != "offline" {
			t.Fatalf("stale peer status = %s", n.Status)
		}
	}
}

func TestAssignNodeLeastLoaded(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "a.db"))
	cfg := config.ClusterConfig{Enabled: true, NodeID: "node-a"}
	svc := NewService(cfg, db)
	svc.registerSelf()
	svc.Accept(model.ClusterNode{NodeID: "node-b", Name: "B"})

	// Load node-a with two devices; node-b has none -> assignment should pick node-b.
	db.Create(&model.Device{Name: "d1", Protocol: "rtsp", NodeID: "node-a"})
	db.Create(&model.Device{Name: "d2", Protocol: "rtsp", NodeID: "node-a"})
	if got := svc.AssignNode(); got != "node-b" {
		t.Fatalf("AssignNode = %q, want node-b", got)
	}

	loads, err := svc.NodeLoads()
	if err != nil {
		t.Fatalf("loads: %v", err)
	}
	for _, l := range loads {
		if l.NodeID == "node-a" && l.Devices != 2 {
			t.Fatalf("node-a devices = %d, want 2", l.Devices)
		}
	}
}

func TestAssignNodeDisabled(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "off.db"))
	svc := NewService(config.ClusterConfig{Enabled: false, NodeID: "solo"}, db)
	if got := svc.AssignNode(); got != "solo" {
		t.Fatalf("AssignNode = %q, want solo", got)
	}
}
