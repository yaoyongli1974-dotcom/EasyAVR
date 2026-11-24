package server

import (
	"github.com/gin-gonic/gin"
)

func (a *App) storageHealth(c *gin.Context) {
	ok(c, a.storage.Health())
}

func (a *App) storageUsage(c *gin.Context) {
	ok(c, gin.H{"dirs": a.storage.Usage()})
}

func (a *App) storageAlerts(c *gin.Context) {
	ok(c, gin.H{"alerts": a.storage.Alerts()})
}
