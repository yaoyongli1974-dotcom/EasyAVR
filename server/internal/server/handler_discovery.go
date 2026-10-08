package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/discovery"
	"github.com/easyavr/easyavr/internal/model"
)

type discoveryRequest struct {
	Mode       string `json:"mode"`       // onvif | subnet | all
	Subnet     string `json:"subnet"`     // CIDR, required for subnet mode
	TimeoutSec int    `json:"timeoutSec"` // discovery window per method
}

// discoverDevices actively scans the network for cameras (ONVIF WS-Discovery
// and/or a bounded TCP port scan) and marks ones already registered.
func (a *App) discoverDevices(c *gin.Context) {
	var req discoveryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Mode == "" {
		req.Mode = "all"
	}
	if req.TimeoutSec <= 0 || req.TimeoutSec > 30 {
		req.TimeoutSec = 3
	}
	timeout := time.Duration(req.TimeoutSec) * time.Second

	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+30*time.Second)
	defer cancel()

	var results []discovery.Device
	var firstErr error

	if req.Mode == "onvif" || req.Mode == "all" {
		items, err := discovery.ProbeONVIF(ctx, timeout)
		if err != nil {
			firstErr = err
			log.Printf("[discovery] onvif probe failed: %v", err)
		}
		results = append(results, items...)
	}

	wantSubnet := req.Mode == "subnet" || (req.Mode == "all" && req.Subnet != "")
	if wantSubnet {
		if req.Subnet == "" {
			fail(c, http.StatusBadRequest, "subnet (CIDR) is required")
			return
		}
		items, err := discovery.ScanSubnetTCP(ctx, req.Subnet, 600*time.Millisecond)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		results = append(results, items...)
	}

	for i := range results {
		var count int64
		a.db.Model(&model.Device{}).Where("ip = ?", results[i].IP).Count(&count)
		results[i].Added = count > 0
	}

	if len(results) == 0 && firstErr != nil {
		fail(c, http.StatusBadGateway, firstErr.Error())
		return
	}
	ok(c, results)
}
