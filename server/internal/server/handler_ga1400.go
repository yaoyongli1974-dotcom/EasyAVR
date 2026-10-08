package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- GA/T1400 cascades ----

func (a *App) listGACascades(c *gin.Context) {
	var items []model.GA1400Cascade
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createGACascade(c *gin.Context) {
	var item model.GA1400Cascade
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if item.URL == "" {
		fail(c, http.StatusBadRequest, "url is required")
		return
	}
	if item.Direction == "" {
		item.Direction = "up"
	}
	if err := a.db.Create(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if a.gaCas != nil && item.Enabled {
		a.gaCas.Ensure(item)
	}
	ok(c, item)
}

func (a *App) testGACascade(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var item model.GA1400Cascade
	if err := a.db.First(&item, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	if a.gaCas == nil {
		fail(c, http.StatusBadRequest, "GA1400 is disabled")
		return
	}
	if err := a.gaCas.Test(item); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "ok": true})
}

// syncGACascade triggers a full sync with the peer view library.
func (a *App) syncGACascade(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var item model.GA1400Cascade
	if err := a.db.First(&item, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	if a.gaCas == nil {
		fail(c, http.StatusBadRequest, "GA1400 is disabled")
		return
	}
	count := a.gaCas.FullSync(item)
	ok(c, gin.H{"id": id, "count": count})
}

func (a *App) deleteGACascade(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if a.gaCas != nil {
		a.gaCas.Remove(id)
	}
	a.db.Where("cascade_id = ?", id).Delete(&model.GA1400Subscription{})
	a.db.Delete(&model.GA1400Cascade{}, id)
	ok(c, gin.H{"id": id})
}

func (a *App) listGASubscriptions(c *gin.Context) {
	var items []model.GA1400Subscription
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}
