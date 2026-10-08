package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// listVideoResources returns catalog entries from the Video Resource Center.
func (a *App) listVideoResources(c *gin.Context) {
	page, pageSize := pagination(c)
	tx := a.db.Model(&model.VideoResource{})
	if kind := c.Query("kind"); kind != "" {
		tx = tx.Where("kind = ?", kind)
	}
	if chID := c.Query("channelId"); chID != "" {
		tx = tx.Where("channel_id = ?", chID)
	}
	var total int64
	tx.Count(&total)
	var items []model.VideoResource
	tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items)
	ok(c, gin.H{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

// videoStats summarizes the video plane: channels online, active streams and
// resource counts.
func (a *App) videoStats(c *gin.Context) {
	var channelTotal, channelOnline int64
	a.db.Model(&model.Channel{}).Count(&channelTotal)
	a.db.Model(&model.Channel{}).Where("online = ?", true).Count(&channelOnline)

	var resourceTotal int64
	a.db.Model(&model.VideoResource{}).Count(&resourceTotal)

	active := 0
	zlmHealthy := false
	if streams, err := a.zlm.MediaList(); err == nil {
		active = len(streams)
		zlmHealthy = true
	}
	ok(c, gin.H{
		"channelTotal":  channelTotal,
		"channelOnline": channelOnline,
		"activeStreams": active,
		"resourceTotal": resourceTotal,
		"zlmHealthy":    zlmHealthy,
	})
}

// liveStreams proxies the streaming core's active stream list.
func (a *App) liveStreams(c *gin.Context) {
	streams, err := a.zlm.MediaList()
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, streams)
}

// syncChannels reconciles channel online state with the streaming core.
func (a *App) syncChannels(c *gin.Context) {
	updated, err := a.devices.SyncStatus()
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"updated": updated})
}
