package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- groups ----

func (a *App) listGroups(c *gin.Context) {
	var groups []model.DeviceGroup
	a.db.Order("path, sort, id").Find(&groups)
	ok(c, groups)
}

type groupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    uint   `json:"parentId"`
	Sort        int    `json:"sort"`
}

func (a *App) createGroup(c *gin.Context) {
	var req groupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	var parent model.DeviceGroup
	path := "/"
	if req.ParentID > 0 {
		if err := a.db.First(&parent, req.ParentID).Error; err != nil {
			fail(c, http.StatusBadRequest, "parent not found")
			return
		}
		path = parent.Path + strconv.FormatUint(uint64(parent.ID), 10) + "/"
	}
	g := model.DeviceGroup{Name: req.Name, Description: req.Description, ParentID: req.ParentID, Path: path, Sort: req.Sort}
	if err := a.db.Create(&g).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// Update path with own ID
	g.Path = path + strconv.FormatUint(uint64(g.ID), 10) + "/"
	a.db.Save(&g)
	ok(c, g)
}

func (a *App) updateGroup(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var g model.DeviceGroup
	if err := a.db.First(&g, id).Error; err != nil {
		fail(c, http.StatusNotFound, "group not found")
		return
	}
	var req groupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Sort != 0 {
		updates["sort"] = req.Sort
	}
	// Parent change requires path rebuild
	if req.ParentID != g.ParentID {
		var parent model.DeviceGroup
		newPath := "/"
		if req.ParentID > 0 {
			if err := a.db.First(&parent, req.ParentID).Error; err != nil {
				fail(c, http.StatusBadRequest, "parent not found")
				return
			}
			if parent.ID == g.ID || strings.HasPrefix(parent.Path, g.Path) {
				fail(c, http.StatusBadRequest, "cannot move group into its own subtree")
				return
			}
			newPath = parent.Path + strconv.FormatUint(uint64(parent.ID), 10) + "/"
		}
		oldPath := g.Path
		newFullPath := newPath + strconv.FormatUint(uint64(g.ID), 10) + "/"
		updates["parent_id"] = req.ParentID
		updates["path"] = newFullPath
		if err := a.db.Model(&g).Updates(updates).Error; err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		// Update all descendants' paths
		a.updateDescendantPaths(oldPath, newFullPath)
		a.db.First(&g, id)
		ok(c, g)
		return
	}
	if len(updates) > 0 {
		a.db.Model(&g).Updates(updates)
	}
	a.db.First(&g, id)
	ok(c, g)
}

func (a *App) updateDescendantPaths(oldPath, newPath string) {
	var descs []model.DeviceGroup
	a.db.Where("path LIKE ?", oldPath+"%").Find(&descs)
	for _, d := range descs {
		newP := strings.Replace(d.Path, oldPath, newPath, 1)
		a.db.Model(&d).Update("path", newP)
	}
}

func (a *App) deleteGroup(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var g model.DeviceGroup
	if err := a.db.First(&g, id).Error; err != nil {
		fail(c, http.StatusNotFound, "group not found")
		return
	}
	// Check for children
	var count int64
	a.db.Model(&model.DeviceGroup{}).Where("parent_id = ?", id).Count(&count)
	if count > 0 {
		fail(c, http.StatusBadRequest, "group has children, delete them first")
		return
	}
	// Check for devices/channels/users
	a.db.Where("group_id = ?", id).Delete(&model.DeviceGroupDevice{})
	a.db.Where("group_id = ?", id).Delete(&model.ChannelGroupChannel{})
	a.db.Where("group_id = ?", id).Delete(&model.UserGroup{})
	a.db.Delete(&g)
	ok(c, gin.H{"id": id})
}

// ---- device-group binding ----

type deviceGroupBindReq struct {
	DeviceIDs []uint `json:"deviceIds"`
	GroupID   uint   `json:"groupId"`
}

func (a *App) bindDevicesToGroup(c *gin.Context) {
	var req deviceGroupBindReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || len(req.DeviceIDs) == 0 {
		fail(c, http.StatusBadRequest, "groupId and deviceIds required")
		return
	}
	var g model.DeviceGroup
	if err := a.db.First(&g, req.GroupID).Error; err != nil {
		fail(c, http.StatusNotFound, "group not found")
		return
	}
	// Remove existing bindings for these devices to this group (idempotent)
	a.db.Where("group_id = ? AND device_id IN ?", req.GroupID, req.DeviceIDs).Delete(&model.DeviceGroupDevice{})
	var bindings []model.DeviceGroupDevice
	for _, did := range req.DeviceIDs {
		bindings = append(bindings, model.DeviceGroupDevice{DeviceID: did, GroupID: req.GroupID})
	}
	if err := a.db.Create(&bindings).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"count": len(bindings)})
}

func (a *App) unbindDevicesFromGroup(c *gin.Context) {
	var req deviceGroupBindReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || len(req.DeviceIDs) == 0 {
		fail(c, http.StatusBadRequest, "groupId and deviceIds required")
		return
	}
	a.db.Where("group_id = ? AND device_id IN ?", req.GroupID, req.DeviceIDs).Delete(&model.DeviceGroupDevice{})
	ok(c, gin.H{"groupId": req.GroupID})
}

func (a *App) listGroupDevices(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var devices []model.Device
	a.db.Joins("JOIN device_group_devices ON device_group_devices.device_id = devices.id").
		Where("device_group_devices.group_id = ?", id).Find(&devices)
	ok(c, devices)
}

// ---- channel-group binding ----

type channelGroupBindReq struct {
	ChannelIDs []uint `json:"channelIds"`
	GroupID    uint   `json:"groupId"`
}

func (a *App) bindChannelsToGroup(c *gin.Context) {
	var req channelGroupBindReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || len(req.ChannelIDs) == 0 {
		fail(c, http.StatusBadRequest, "groupId and channelIds required")
		return
	}
	var g model.DeviceGroup
	if err := a.db.First(&g, req.GroupID).Error; err != nil {
		fail(c, http.StatusNotFound, "group not found")
		return
	}
	a.db.Where("group_id = ? AND channel_id IN ?", req.GroupID, req.ChannelIDs).Delete(&model.ChannelGroupChannel{})
	var bindings []model.ChannelGroupChannel
	for _, cid := range req.ChannelIDs {
		bindings = append(bindings, model.ChannelGroupChannel{ChannelID: cid, GroupID: req.GroupID})
	}
	if err := a.db.Create(&bindings).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"count": len(bindings)})
}

func (a *App) unbindChannelsFromGroup(c *gin.Context) {
	var req channelGroupBindReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || len(req.ChannelIDs) == 0 {
		fail(c, http.StatusBadRequest, "groupId and channelIds required")
		return
	}
	a.db.Where("group_id = ? AND channel_id IN ?", req.GroupID, req.ChannelIDs).Delete(&model.ChannelGroupChannel{})
	ok(c, gin.H{"groupId": req.GroupID})
}

func (a *App) listGroupChannels(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var channels []model.Channel
	a.db.Joins("JOIN channel_group_channels ON channel_group_channels.channel_id = channels.id").
		Where("channel_group_channels.group_id = ?", id).Find(&channels)
	ok(c, channels)
}

// ---- user-group permissions ----

type userGroupReq struct {
	UserID      uint   `json:"userId"`
	GroupID     uint   `json:"groupId"`
	Permissions string `json:"permissions"`
}

func (a *App) listUserGroups(c *gin.Context) {
	var ugs []model.UserGroup
	a.db.Preload("User").Preload("Group").Find(&ugs)
	ok(c, ugs)
}

func (a *App) assignUserGroup(c *gin.Context) {
	var req userGroupReq
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == 0 || req.GroupID == 0 {
		fail(c, http.StatusBadRequest, "userId and groupId required")
		return
	}
	var u model.User
	if err := a.db.First(&u, req.UserID).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	var g model.DeviceGroup
	if err := a.db.First(&g, req.GroupID).Error; err != nil {
		fail(c, http.StatusNotFound, "group not found")
		return
	}
	ug := model.UserGroup{UserID: req.UserID, GroupID: req.GroupID, Permissions: req.Permissions}
	if err := a.db.Where("user_id = ? AND group_id = ?", req.UserID, req.GroupID).
		Assign(ug).FirstOrCreate(&ug).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, ug)
}

func (a *App) updateUserGroup(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ug model.UserGroup
	if err := a.db.First(&ug, id).Error; err != nil {
		fail(c, http.StatusNotFound, "user-group not found")
		return
	}
	var req userGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Permissions != "" {
		ug.Permissions = req.Permissions
		a.db.Save(&ug)
	}
	ok(c, ug)
}

func (a *App) deleteUserGroup(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.UserGroup{}, id)
	ok(c, gin.H{"id": id})
}
