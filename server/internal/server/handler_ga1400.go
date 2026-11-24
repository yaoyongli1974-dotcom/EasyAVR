package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- GA/T1400 cascades ----

// gaCascadeReq is the write payload for a GA/T1400 cascade. password is
// write-only: an empty value keeps the stored one, clearPassword removes it.
type gaCascadeReq struct {
	Name          string `json:"name"`
	Direction     string `json:"direction"`
	PlatformID    string `json:"platformId"`
	URL           string `json:"url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clearPassword"`
	Enabled       bool   `json:"enabled"`
}

type gaCascadeView struct {
	model.GA1400Cascade
	PasswordConfigured bool `json:"passwordConfigured"`
}

func gaCascadeViewOf(item model.GA1400Cascade) gaCascadeView {
	return gaCascadeView{GA1400Cascade: item, PasswordConfigured: item.Password != ""}
}

func (a *App) listGACascades(c *gin.Context) {
	var items []model.GA1400Cascade
	a.db.Order("id DESC").Find(&items)
	views := make([]gaCascadeView, 0, len(items))
	for _, item := range items {
		views = append(views, gaCascadeViewOf(item))
	}
	ok(c, views)
}

func (a *App) createGACascade(c *gin.Context) {
	var req gaCascadeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.URL == "" {
		fail(c, http.StatusBadRequest, "url is required")
		return
	}
	item := model.GA1400Cascade{
		Name: req.Name, Direction: req.Direction, PlatformID: req.PlatformID,
		URL: req.URL, Username: req.Username, Password: req.Password, Enabled: req.Enabled,
	}
	if item.Direction == "" {
		item.Direction = "up"
	}
	if err := a.db.Create(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// Reload so outbound registration uses the decrypted password.
	var fresh model.GA1400Cascade
	if err := a.db.First(&fresh, item.ID).Error; err != nil {
		fresh = item
	} else if a.gaCas != nil && fresh.Enabled {
		a.gaCas.Ensure(fresh)
	}
	ok(c, gaCascadeViewOf(fresh))
}

func (a *App) updateGACascade(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var item model.GA1400Cascade
	if err := a.db.First(&item, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	var req gaCascadeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name != "" {
		item.Name = req.Name
	}
	if req.Direction != "" {
		item.Direction = req.Direction
	}
	if req.PlatformID != "" {
		item.PlatformID = req.PlatformID
	}
	if req.URL != "" {
		item.URL = req.URL
	}
	if req.Username != "" {
		item.Username = req.Username
	}
	switch {
	case req.Password != "":
		item.Password = req.Password
	case req.ClearPassword:
		item.Password = ""
	}
	item.Enabled = req.Enabled
	if err := a.db.Save(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var fresh model.GA1400Cascade
	if err := a.db.First(&fresh, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	if a.gaCas != nil {
		if fresh.Enabled {
			a.gaCas.Ensure(fresh)
		} else {
			a.gaCas.Remove(id)
		}
	}
	ok(c, gaCascadeViewOf(fresh))
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
