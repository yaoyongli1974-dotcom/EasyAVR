package server

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// cloudStoreReq is the write payload for a storage config. SecretKey is
// write-only: it is accepted here but never serialized back to clients.
type cloudStoreReq struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Region    string `json:"region"`
	PathStyle *bool  `json:"pathStyle"`
	Enabled   *bool  `json:"enabled"`
	StoreID   uint   `json:"storeId"`
}

func maskStore(s model.CloudStore) model.CloudStore {
	s.HasSecret = s.SecretKey != ""
	s.SecretKey = ""
	return s
}

func (a *App) listStores(c *gin.Context) {
	var items []model.CloudStore
	a.db.Order("id DESC").Find(&items)
	for i := range items {
		items[i] = maskStore(items[i])
	}
	ok(c, items)
}

func (a *App) createStore(c *gin.Context) {
	var req cloudStoreReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" || req.Endpoint == "" || req.Bucket == "" {
		fail(c, http.StatusBadRequest, "name, endpoint and bucket are required")
		return
	}
	store := model.CloudStore{
		Name:      req.Name,
		Type:      req.Type,
		Endpoint:  req.Endpoint,
		Bucket:    req.Bucket,
		AccessKey: req.AccessKey,
		SecretKey: req.SecretKey,
		Region:    req.Region,
		PathStyle: true,
		Enabled:   true,
	}
	if store.Type == "" {
		store.Type = "s3"
	}
	if req.PathStyle != nil {
		store.PathStyle = *req.PathStyle
	}
	if req.Enabled != nil {
		store.Enabled = *req.Enabled
	}
	if err := a.db.Create(&store).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, maskStore(store))
}

func (a *App) updateStore(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var store model.CloudStore
	if err := a.db.First(&store, id).Error; err != nil {
		fail(c, http.StatusNotFound, "storage not found")
		return
	}
	var req cloudStoreReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{
		"name": req.Name, "type": req.Type, "endpoint": req.Endpoint,
		"bucket": req.Bucket, "access_key": req.AccessKey, "region": req.Region,
	}
	if req.Type == "" {
		updates["type"] = "s3"
	}
	// An empty secret means "keep the stored one".
	if req.SecretKey != "" {
		updates["secret_key"] = req.SecretKey
	}
	if req.PathStyle != nil {
		updates["path_style"] = *req.PathStyle
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	a.db.Model(&store).Updates(updates)
	a.db.First(&store, id)
	a.arch.Invalidate(id)
	ok(c, maskStore(store))
}

func (a *App) deleteStore(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.CloudStore{}, id)
	a.arch.Invalidate(id)
	ok(c, gin.H{"id": id})
}

// bindStoreSettings resolves a store config from a stored row and/or inline
// body fields, so credentials can be tested before they are saved.
func (a *App) bindStoreSettings(c *gin.Context) (model.CloudStore, bool) {
	var req cloudStoreReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return model.CloudStore{}, false
	}
	id := req.StoreID
	if p := c.Param("id"); p != "" {
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid id")
			return model.CloudStore{}, false
		}
		id = uint(v)
	}
	store := model.CloudStore{PathStyle: true, Enabled: true}
	if id != 0 {
		if err := a.db.First(&store, id).Error; err != nil {
			fail(c, http.StatusNotFound, "storage not found")
			return model.CloudStore{}, false
		}
	}
	if req.Name != "" {
		store.Name = req.Name
	}
	if req.Type != "" {
		store.Type = req.Type
	}
	if req.Endpoint != "" {
		store.Endpoint = req.Endpoint
	}
	if req.Bucket != "" {
		store.Bucket = req.Bucket
	}
	if req.AccessKey != "" {
		store.AccessKey = req.AccessKey
	}
	if req.SecretKey != "" {
		store.SecretKey = req.SecretKey
	}
	if req.Region != "" {
		store.Region = req.Region
	}
	if req.PathStyle != nil {
		store.PathStyle = *req.PathStyle
	}
	if req.Enabled != nil {
		store.Enabled = *req.Enabled
	}
	if store.Type == "" {
		store.Type = "s3"
	}
	if store.Endpoint == "" || store.Bucket == "" {
		fail(c, http.StatusBadRequest, "endpoint and bucket are required")
		return model.CloudStore{}, false
	}
	return store, true
}

func (a *App) testStore(c *gin.Context) {
	store, valid := a.bindStoreSettings(c)
	if !valid {
		return
	}
	if !store.Enabled {
		fail(c, http.StatusBadRequest, "storage is disabled")
		return
	}
	if err := a.arch.Test(c.Request.Context(), store); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"id": store.ID, "ok": true})
}

// archiveRecording uploads one catalog recording to a cloud store.
func (a *App) archiveRecording(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var req struct {
		StoreID uint `json:"storeId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.StoreID == 0 {
		fail(c, http.StatusBadRequest, "storeId is required")
		return
	}
	arch, err := a.arch.Upload(id, req.StoreID)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, arch)
}

func (a *App) listRecordingArchives(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	items, err := a.arch.List(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, items)
}

func (a *App) listArchives(c *gin.Context) {
	page, pageSize := pagination(c)
	items, total, err := a.arch.ListByStatus(c.Query("status"), page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "pageSize": pageSize})
}
