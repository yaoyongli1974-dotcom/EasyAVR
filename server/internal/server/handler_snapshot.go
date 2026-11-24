package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/auth"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/snapshot"
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

// snapshotAccessURL issues a short-lived signed URL for a snapshot the caller
// is allowed to see. It accepts either a numeric :id or an exact ?url= (for
// clients that only know the legacy /snapshots/<channelId>/<ms>.jpg path,
// e.g. /api/v1/snapshots/0/access-url?url=<encoded original>).
func (a *App) snapshotAccessURL(c *gin.Context) {
	claims := currentClaims(c)
	if claims == nil {
		fail(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var snap model.Snapshot
	if rawURL := c.Query("url"); rawURL != "" {
		if err := a.db.Where("url = ?", rawURL).First(&snap).Error; err != nil {
			fail(c, http.StatusNotFound, "snapshot not found")
			return
		}
	} else {
		id, valid := parseUintParam(c, "id")
		if !valid {
			return
		}
		if err := a.db.First(&snap, id).Error; err != nil {
			fail(c, http.StatusNotFound, "snapshot not found")
			return
		}
	}
	if !a.snapshotChannelAllowed(claims, snap.ChannelID) {
		fail(c, http.StatusForbidden, "权限不足：video")
		return
	}
	token, expiresAt, err := snapshot.SnapshotAccessToken(snap.ID, claims.UserID, snapshot.DefaultTokenTTL, a.cfg.JWTSecret)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"url": "/api/v1/snapshot-access/" + token, "expiresAt": expiresAt})
}

// snapshotSignedAccess serves a snapshot image without a JWT. The signed token
// carries the snapshot ID, user ID and expiry; the file path always comes from
// the catalog and is confined to the snapshot storage directory.
func (a *App) snapshotSignedAccess(c *gin.Context) {
	id, _, _, err := snapshot.ParseSnapshotAccessToken(c.Param("token"), a.cfg.JWTSecret)
	if err != nil {
		fail(c, http.StatusUnauthorized, "invalid or expired access token")
		return
	}
	var snap model.Snapshot
	if err := a.db.First(&snap, id).Error; err != nil {
		fail(c, http.StatusNotFound, "snapshot not found")
		return
	}
	path, err := a.snaps.ResolvePath(snap.Path)
	if err != nil {
		fail(c, http.StatusForbidden, "invalid snapshot path")
		return
	}
	c.File(path)
}

func (a *App) listSnapshots(c *gin.Context) {
	var channelID uint
	if v := c.Query("channelId"); v != "" {
		channelID = parseChannelID(v)
	}
	page, pageSize := pagination(c)
	allowed := a.snapshotVisibleChannels(currentClaims(c))
	items, total, err := a.snaps.ListForChannels(channelID, allowed, page, pageSize)
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
	var snap model.Snapshot
	if err := a.db.First(&snap, id).Error; err != nil {
		fail(c, http.StatusNotFound, "snapshot not found")
		return
	}
	if !a.snapshotChannelAllowed(currentClaims(c), snap.ChannelID) {
		fail(c, http.StatusForbidden, "权限不足：video")
		return
	}
	if err := a.snaps.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

// snapshotChannelAllowed mirrors requireGroupPerm for snapshot ownership:
// admin and role-level "video" pass; otherwise the channel's groups must
// overlap the user's authorised groups. Channels in no group fall back to the
// policy engine's "video" permission.
func (a *App) snapshotChannelAllowed(claims *auth.Claims, channelID uint) bool {
	if claims == nil {
		return false
	}
	if claims.Role == "admin" {
		return true
	}
	var role model.Role
	if a.db.Where("name = ?", claims.Role).First(&role).Error == nil && hasPerm(role.Permissions, "video") {
		return true
	}
	userGroups := a.snapshotUserGroups(claims.UserID)
	for _, gid := range a.snapshotChannelGroups(channelID) {
		for _, ug := range userGroups {
			if gid == ug {
				return true
			}
		}
	}
	if len(a.snapshotChannelGroups(channelID)) == 0 {
		return a.snapshotPolicyAllows(claims)
	}
	return false
}

// snapshotVisibleChannels returns channel IDs whose snapshots may be listed,
// or nil when every channel is visible. Channels are allowed when they belong
// to one of the user's authorised groups, or (when they belong to no group at
// all) when the policy engine grants "video".
func (a *App) snapshotVisibleChannels(claims *auth.Claims) []uint {
	if claims == nil {
		return []uint{}
	}
	if claims.Role == "admin" {
		return nil
	}
	var role model.Role
	if a.db.Where("name = ?", claims.Role).First(&role).Error == nil && hasPerm(role.Permissions, "video") {
		return nil
	}
	allowed := a.snapshotGroupChannels(a.snapshotUserGroups(claims.UserID))
	if a.snapshotPolicyAllows(claims) {
		var allGroups, allChannels []uint
		a.db.Model(&model.DeviceGroup{}).Pluck("id", &allGroups)
		a.db.Model(&model.Channel{}).Pluck("id", &allChannels)
		grouped := make(map[uint]bool)
		for _, id := range a.snapshotGroupChannels(allGroups) {
			grouped[id] = true
		}
		for _, id := range allChannels {
			if !grouped[id] {
				allowed = append(allowed, id)
			}
		}
	}
	return uniqueUint(allowed)
}

func (a *App) snapshotUserGroups(userID uint) []uint {
	var ugs []model.UserGroup
	a.db.Where("user_id = ?", userID).Find(&ugs)
	var ids []uint
	for _, ug := range ugs {
		// Empty per-group permissions inherit the role, as in requireGroupPerm.
		if ug.Permissions == "" || hasPerm(ug.Permissions, "video") {
			ids = append(ids, ug.GroupID)
		}
	}
	return uniqueUint(ids)
}

// snapshotChannelGroups lists groups containing a channel, directly or via
// its device (both the many-to-many binding and Device.GroupID).
func (a *App) snapshotChannelGroups(channelID uint) []uint {
	var ids []uint
	a.db.Model(&model.ChannelGroupChannel{}).Where("channel_id = ?", channelID).Pluck("group_id", &ids)
	var deviceID uint
	a.db.Model(&model.Channel{}).Where("id = ?", channelID).Pluck("device_id", &deviceID)
	if deviceID > 0 {
		var deviceGroups []uint
		a.db.Model(&model.DeviceGroupDevice{}).Where("device_id = ?", deviceID).Pluck("group_id", &deviceGroups)
		ids = append(ids, deviceGroups...)
		var direct uint
		a.db.Model(&model.Device{}).Where("id = ?", deviceID).Pluck("group_id", &direct)
		if direct > 0 {
			ids = append(ids, direct)
		}
	}
	return uniqueUint(ids)
}

func (a *App) snapshotGroupChannels(groupIDs []uint) []uint {
	if len(groupIDs) == 0 {
		return nil
	}
	var channels []uint
	a.db.Model(&model.ChannelGroupChannel{}).Where("group_id IN ?", groupIDs).Pluck("channel_id", &channels)
	var devices []uint
	a.db.Model(&model.DeviceGroupDevice{}).Where("group_id IN ?", groupIDs).Pluck("device_id", &devices)
	a.db.Model(&model.Device{}).Where("group_id IN ?", groupIDs).Pluck("id", &devices)
	devices = uniqueUint(devices)
	if len(devices) > 0 {
		var byDevice []uint
		a.db.Model(&model.Channel{}).Where("device_id IN ?", devices).Pluck("id", &byDevice)
		channels = append(channels, byDevice...)
	}
	return uniqueUint(channels)
}

func (a *App) snapshotPolicyAllows(claims *auth.Claims) bool {
	allowed, err := a.policy.CheckPermissionForUser(claims.UserID, claims.Username, claims.Role, "video", "read", "")
	return err == nil && allowed
}

func uniqueUint(ids []uint) []uint {
	seen := make(map[uint]bool, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
