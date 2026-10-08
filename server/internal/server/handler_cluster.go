package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

func (a *App) clusterNodes(c *gin.Context) {
	nodes, err := a.cluster.Nodes()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, nodes)
}

// clusterStats returns nodes with their assigned device counts.
func (a *App) clusterStats(c *gin.Context) {
	loads, err := a.cluster.NodeLoads()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, loads)
}

func (a *App) clusterConfig(c *gin.Context) {
	ok(c, gin.H{
		"enabled":    a.cluster.Enabled(),
		"nodeId":     a.cfg.Cluster.NodeID,
		"name":       a.cfg.Cluster.Name,
		"apiBase":    a.cfg.Cluster.APIBase,
		"peers":      a.cfg.Cluster.Peers,
		"database":   a.cfg.DBPath,
		"assignment": a.cfg.Cluster.Enabled,
	})
}

// clusterHeartbeat receives heartbeats from peer nodes. It is mounted outside
// the JWT middleware and authenticated with the shared cluster secret.
func (a *App) clusterHeartbeat(c *gin.Context) {
	if secret := a.cluster.Secret(); secret != "" && c.GetHeader("X-Cluster-Secret") != secret {
		fail(c, http.StatusUnauthorized, "invalid cluster secret")
		return
	}
	var node model.ClusterNode
	if err := c.ShouldBindJSON(&node); err != nil || node.NodeID == "" {
		fail(c, http.StatusBadRequest, "invalid node")
		return
	}
	a.cluster.Accept(node)
	ok(c, gin.H{"accepted": node.NodeID})
}
