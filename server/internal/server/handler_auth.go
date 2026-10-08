package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (a *App) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "username and password are required")
		return
	}
	var user model.User
	if err := a.db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		fail(c, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !user.Enabled || !store.CheckPassword(user.PasswordHash, req.Password) {
		fail(c, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := a.jwt.Issue(user.ID, user.Username, user.Role)
	if err != nil {
		fail(c, http.StatusInternalServerError, "failed to issue token")
		return
	}
	ok(c, gin.H{"token": token, "user": user})
}

func (a *App) profile(c *gin.Context) {
	claims := currentClaims(c)
	if claims == nil {
		fail(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var user model.User
	if err := a.db.First(&user, claims.UserID).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	ok(c, user)
}
