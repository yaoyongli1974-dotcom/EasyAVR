package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

// ---- users ----

func (a *App) listUsers(c *gin.Context) {
	var users []model.User
	tx := a.db.Order("id")
	if role := c.Query("role"); role != "" {
		tx = tx.Where("role = ?", role)
	}
	if kw := c.Query("keyword"); kw != "" {
		like := "%" + kw + "%"
		tx = tx.Where("username LIKE ? OR nickname LIKE ?", like, like)
	}
	tx.Find(&users)
	ok(c, users)
}

type userRequest struct {
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Enabled  *bool  `json:"enabled"`
}

func (a *App) createUser(c *gin.Context) {
	var req userRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		fail(c, http.StatusBadRequest, "username and password are required")
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	if !a.roleExists(req.Role) {
		fail(c, http.StatusBadRequest, "role not found: "+req.Role)
		return
	}
	var count int64
	a.db.Model(&model.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		fail(c, http.StatusConflict, "username already exists")
		return
	}
	hash, err := store.HashPassword(req.Password)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	u := model.User{Username: req.Username, Nickname: req.Nickname, PasswordHash: hash, Role: req.Role, Enabled: true}
	if req.Enabled != nil {
		u.Enabled = *req.Enabled
	}
	if err := a.db.Create(&u).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, u)
}

func (a *App) updateUser(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var u model.User
	if err := a.db.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	var req userRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Nickname != "" {
		updates["nickname"] = req.Nickname
	}
	if req.Role != "" {
		if !a.roleExists(req.Role) {
			fail(c, http.StatusBadRequest, "role not found: "+req.Role)
			return
		}
		// Do not allow demoting the last admin.
		if u.Role == "admin" && req.Role != "admin" && a.countAdmins() <= 1 {
			fail(c, http.StatusBadRequest, "不能修改最后一个管理员为其他角色")
			return
		}
		updates["role"] = req.Role
	}
	if req.Enabled != nil {
		if claims := currentClaims(c); claims != nil && claims.UserID == u.ID && !*req.Enabled {
			fail(c, http.StatusBadRequest, "不能禁用当前登录用户")
			return
		}
		updates["enabled"] = *req.Enabled
	}
	if req.Password != "" {
		hash, err := store.HashPassword(req.Password)
		if err != nil {
			fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		updates["password_hash"] = hash
	}
	if len(updates) > 0 {
		a.db.Model(&u).Updates(updates)
	}
	a.db.First(&u, id)
	ok(c, u)
}

func (a *App) deleteUser(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if claims := currentClaims(c); claims != nil && claims.UserID == id {
		fail(c, http.StatusBadRequest, "不能删除当前登录用户")
		return
	}
	var u model.User
	if err := a.db.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	if u.Role == "admin" && a.countAdmins() <= 1 {
		fail(c, http.StatusBadRequest, "不能删除最后一个管理员")
		return
	}
	a.db.Delete(&model.User{}, id)
	ok(c, gin.H{"id": id})
}

// changePassword lets the current user change their own password.
func (a *App) changePassword(c *gin.Context) {
	claims := currentClaims(c)
	if claims == nil {
		fail(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.NewPassword == "" {
		fail(c, http.StatusBadRequest, "newPassword is required")
		return
	}
	var u model.User
	if err := a.db.First(&u, claims.UserID).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	if !store.CheckPassword(u.PasswordHash, req.OldPassword) {
		fail(c, http.StatusBadRequest, "原密码错误")
		return
	}
	hash, err := store.HashPassword(req.NewPassword)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.db.Model(&u).Update("password_hash", hash)
	ok(c, gin.H{"id": u.ID})
}

func (a *App) roleExists(name string) bool {
	if name == "admin" {
		return true
	}
	var count int64
	a.db.Model(&model.Role{}).Where("name = ?", name).Count(&count)
	return count > 0
}

func (a *App) countAdmins() int64 {
	var count int64
	a.db.Model(&model.User{}).Where("role = ? AND enabled = ?", "admin", true).Count(&count)
	return count
}

// ---- roles ----

func (a *App) listRoles(c *gin.Context) {
	var roles []model.Role
	a.db.Order("id").Find(&roles)
	ok(c, roles)
}

type roleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Permissions string `json:"permissions"`
}

func (a *App) createRole(c *gin.Context) {
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	var count int64
	a.db.Model(&model.Role{}).Where("name = ?", req.Name).Count(&count)
	if count > 0 {
		fail(c, http.StatusConflict, "role already exists")
		return
	}
	r := model.Role{Name: req.Name, Description: req.Description, Permissions: req.Permissions}
	if err := a.db.Create(&r).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, r)
}

func (a *App) updateRole(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var r model.Role
	if err := a.db.First(&r, id).Error; err != nil {
		fail(c, http.StatusNotFound, "role not found")
		return
	}
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Permissions != "" {
		updates["permissions"] = req.Permissions
	}
	if req.Name != "" && req.Name != r.Name {
		if r.Builtin {
			fail(c, http.StatusBadRequest, "内置角色不可改名")
			return
		}
		updates["name"] = req.Name
	}
	a.db.Model(&r).Updates(updates)
	a.db.First(&r, id)
	ok(c, r)
}

func (a *App) deleteRole(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var r model.Role
	if err := a.db.First(&r, id).Error; err != nil {
		fail(c, http.StatusNotFound, "role not found")
		return
	}
	if r.Builtin || r.Name == "admin" {
		fail(c, http.StatusBadRequest, "内置角色不可删除")
		return
	}
	var count int64
	a.db.Model(&model.User{}).Where("role = ?", r.Name).Count(&count)
	if count > 0 {
		fail(c, http.StatusBadRequest, "该角色下仍有用户，请先移除或改用其他角色")
		return
	}
	a.db.Delete(&model.Role{}, id)
	ok(c, gin.H{"id": id})
}
