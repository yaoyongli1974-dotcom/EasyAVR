// Package cluster implements optional multi-node coordination: nodes register
// with each other and heartbeat, and devices can be assigned to a node so that
// a shared database scales across servers.
package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
)

// Service manages cluster membership.
type Service struct {
	cfg    config.ClusterConfig
	db     *gorm.DB
	client *http.Client
}

func NewService(cfg config.ClusterConfig, db *gorm.DB) *Service {
	return &Service{cfg: cfg, db: db, client: &http.Client{Timeout: 8 * time.Second}}
}

func (s *Service) Enabled() bool { return s.cfg.Enabled }

// Secret returns the shared inter-node secret.
func (s *Service) Secret() string { return s.cfg.Secret }

// registerSelf upserts this node as a cluster member.
func (s *Service) registerSelf() {
	node := model.ClusterNode{
		NodeID: s.cfg.NodeID, Name: s.cfg.Name, APIBase: s.cfg.APIBase,
		Status: "online", IsSelf: true, LastHeartbeat: time.Now(),
	}
	var existing model.ClusterNode
	if err := s.db.Where("node_id = ?", s.cfg.NodeID).First(&existing).Error; err == nil {
		s.db.Model(&existing).Updates(map[string]any{
			"name": node.Name, "api_base": node.APIBase, "status": "online",
			"is_self": true, "last_heartbeat": node.LastHeartbeat,
		})
		return
	}
	s.db.Create(&node)
}

// Accept records a heartbeat from another node.
func (s *Service) Accept(node model.ClusterNode) {
	node.IsSelf = node.NodeID == s.cfg.NodeID
	node.Status = "online"
	node.LastHeartbeat = time.Now()
	var existing model.ClusterNode
	if err := s.db.Where("node_id = ?", node.NodeID).First(&existing).Error; err == nil {
		s.db.Model(&existing).Updates(map[string]any{
			"name": node.Name, "api_base": node.APIBase, "status": "online",
			"last_heartbeat": node.LastHeartbeat,
		})
		return
	}
	s.db.Create(&node)
}

// Nodes returns all nodes with staleness applied to status.
func (s *Service) Nodes() ([]model.ClusterNode, error) {
	var nodes []model.ClusterNode
	if err := s.db.Order("is_self DESC, id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	stale := time.Duration(s.heartbeatSec()*3) * time.Second
	for i := range nodes {
		if !nodes[i].IsSelf && time.Since(nodes[i].LastHeartbeat) > stale {
			nodes[i].Status = "offline"
		}
	}
	return nodes, nil
}

// SelfNodeID returns this node's id.
func (s *Service) SelfNodeID() string { return s.cfg.NodeID }

// AssignNode picks the least-loaded online node (or self when clustering is off).
func (s *Service) AssignNode() string {
	if !s.cfg.Enabled {
		return s.cfg.NodeID
	}
	nodes, _ := s.Nodes()
	best := ""
	bestCount := -1
	for _, n := range nodes {
		if n.Status != "online" {
			continue
		}
		var count int64
		s.db.Model(&model.Device{}).Where("node_id = ?", n.NodeID).Count(&count)
		if bestCount == -1 || int(count) < bestCount {
			best = n.NodeID
			bestCount = int(count)
		}
	}
	if best == "" {
		best = s.cfg.NodeID
	}
	return best
}

// NodeLoad pairs a node with its assigned device count.
type NodeLoad struct {
	model.ClusterNode
	Devices int64 `json:"devices"`
}

// NodeLoads returns every node with its device count.
func (s *Service) NodeLoads() ([]NodeLoad, error) {
	nodes, err := s.Nodes()
	if err != nil {
		return nil, err
	}
	out := make([]NodeLoad, 0, len(nodes))
	for _, n := range nodes {
		var count int64
		s.db.Model(&model.Device{}).Where("node_id = ?", n.NodeID).Count(&count)
		out = append(out, NodeLoad{ClusterNode: n, Devices: count})
	}
	return out, nil
}

// Start registers self and launches the peer heartbeat loop.
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		return
	}
	s.registerSelf()
	log.Printf("[cluster] node %s (%s) started, %d peers", s.cfg.NodeID, s.cfg.APIBase, len(s.cfg.Peers))
	go func() {
		ticker := time.NewTicker(time.Duration(s.heartbeatSec()) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.heartbeat()
			}
		}
	}()
}

func (s *Service) heartbeat() {
	self := model.ClusterNode{
		NodeID: s.cfg.NodeID, Name: s.cfg.Name, APIBase: s.cfg.APIBase,
		Status: "online", LastHeartbeat: time.Now(),
	}
	body, _ := json.Marshal(self)
	for _, peer := range s.cfg.Peers {
		url := trimSlash(peer) + "/api/v1/cluster/heartbeat"
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Cluster-Secret", s.cfg.Secret)
		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
	// Refresh self liveness.
	s.db.Model(&model.ClusterNode{}).Where("node_id = ?", s.cfg.NodeID).
		Update("last_heartbeat", time.Now())
}

func (s *Service) heartbeatSec() int {
	if s.cfg.HeartbeatSec <= 0 {
		return 15
	}
	return s.cfg.HeartbeatSec
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
