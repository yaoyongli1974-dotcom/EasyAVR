package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *App) captureSnapshot(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	snap, err := a.snaps.Capture(c.Request.Context(), id)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, snap)
}

func (a *App) listSnapshots(c *gin.Context) {
	var channelID uint
	if v := c.Query("channelId"); v != "" {
		channelID = parseChannelID(v)
	}
	page, pageSize := pagination(c)
	items, total, err := a.snaps.List(channelID, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

func (a *App) deleteSnapshot(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.snaps.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}
