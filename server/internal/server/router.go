package server

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// buildRouter registers all HTTP routes.
//
// API layout (v1) mirrors the platform planes:
//
//	/auth        authentication
//	/devices     device access plane / video middle platform
//	/video       video resource center
//	/ai          AI middle platform (providers, tasks) + event center
//	/events      AI event center queries
//	/system      health and platform info
func (a *App) buildRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), a.cors())

	api := r.Group("/api/v1")
	api.GET("/healthz", a.healthz)

	authGroup := api.Group("/auth")
	authGroup.POST("/login", a.login)

	// Everything below requires a valid JWT.
	authed := api.Group("")
	authed.Use(a.authRequired())

	authed.GET("/auth/profile", a.profile)
	authed.POST("/auth/password", a.changePassword)

	// User and role management is restricted to administrators.
	users := authed.Group("/users")
	users.Use(a.adminRequired())
	users.GET("", a.listUsers)
	users.POST("", a.createUser)
	users.PUT("/:id", a.updateUser)
	users.DELETE("/:id", a.deleteUser)

	roles := authed.Group("/roles")
	roles.Use(a.adminRequired())
	roles.GET("", a.listRoles)
	roles.POST("", a.createRole)
	roles.PUT("/:id", a.updateRole)
	roles.DELETE("/:id", a.deleteRole)

	// Device group management (admin only).
	groups := authed.Group("/groups")
	groups.Use(a.adminRequired())
	groups.GET("", a.listGroups)
	groups.POST("", a.createGroup)
	groups.PUT("/:id", a.updateGroup)
	groups.DELETE("/:id", a.deleteGroup)
	groups.POST("/devices/bind", a.bindDevicesToGroup)
	groups.POST("/devices/unbind", a.unbindDevicesFromGroup)
	groups.GET("/:id/devices", a.listGroupDevices)
	groups.POST("/channels/bind", a.bindChannelsToGroup)
	groups.POST("/channels/unbind", a.unbindChannelsFromGroup)
	groups.GET("/:id/channels", a.listGroupChannels)
	groups.GET("/user-groups", a.listUserGroups)
	groups.POST("/user-groups", a.assignUserGroup)
	groups.PUT("/user-groups/:id", a.updateUserGroup)
	groups.DELETE("/user-groups/:id", a.deleteUserGroup)

	d := authed.Group("/devices")
	d.Use(a.requireGroupPerm("device"))
	d.GET("", a.listDevices)
	d.POST("", a.createDevice)
	d.GET("/:id", a.getDevice)
	d.PUT("/:id", a.updateDevice)
	d.DELETE("/:id", a.deleteDevice)
	d.POST("/:id/channels", a.createChannel)
	d.GET("/:id/channels", a.listDeviceChannels)

	authed.POST("/discovery/scan", a.discoverDevices)

	onvifGroup := authed.Group("/onvif")
	onvifGroup.POST("/probe", a.onvifProbe)
	onvifGroup.POST("/import", a.onvifImport)

	isapiGroup := authed.Group("/isapi")
	isapiGroup.POST("/probe", a.isapiProbe)
	isapiGroup.POST("/import", a.isapiImport)

	ch := authed.Group("/channels")
	ch.Use(a.requireGroupPerm("video"))
	ch.GET("", a.listChannels)
	ch.PUT("/:id", a.updateChannel)
	ch.DELETE("/:id", a.deleteChannel)
	ch.POST("/:id/start", a.startChannel)
	ch.POST("/:id/stop", a.stopChannel)
	ch.GET("/:id/play-urls", a.channelPlayURLs)
	ch.POST("/:id/record/start", a.startRecording)
	ch.POST("/:id/record/stop", a.stopRecording)
	ch.POST("/:id/snapshot", a.captureSnapshot)
	ch.GET("/:id/recordings", a.listChannelRecordings)
	ch.POST("/:id/recordings/sync", a.syncChannelRecordings)
	ch.GET("/:id/recording-plan", a.getRecordingPlan)
	ch.PUT("/:id/recording-plan", a.upsertRecordingPlan)

	authed.GET("/recordings", a.listRecordings)
	authed.DELETE("/recordings/:id", a.deleteRecording)

	authed.GET("/snapshots", a.listSnapshots)
	authed.DELETE("/snapshots/:id", a.deleteSnapshot)

	authed.GET("/gb/devices", a.listGBDevices)
	authed.GET("/gb/config", a.gbConfig)
	authed.POST("/gb/devices/:gbid/refresh", a.refreshGBCatalog)
	authed.GET("/gb/cascades", a.listCascades)
	authed.POST("/gb/cascades", a.createCascade)
	authed.POST("/gb/cascades/:id/refresh", a.refreshCascade)
	authed.DELETE("/gb/cascades/:id", a.deleteCascade)
	authed.GET("/gb/whitelist", a.listWhiteList)
	authed.POST("/gb/whitelist", a.createWhiteList)
	authed.DELETE("/gb/whitelist/:id", a.deleteWhiteList)

	ga1400Group := authed.Group("/ga1400")
	ga1400Group.GET("/cascades", a.listGACascades)
	ga1400Group.POST("/cascades", a.createGACascade)
	ga1400Group.POST("/cascades/:id/test", a.testGACascade)
	ga1400Group.POST("/cascades/:id/sync", a.syncGACascade)
	ga1400Group.DELETE("/cascades/:id", a.deleteGACascade)
	ga1400Group.GET("/subscriptions", a.listGASubscriptions)

	gb35114Group := authed.Group("/gb35114")
	gb35114Group.Use(a.requirePerm("config"))
	gb35114Group.GET("/config", a.gb35114Config)
	gb35114Group.POST("/platform-cert", a.gb35114GenerateCert)
	gb35114Group.POST("/sign-csr", a.gb35114SignCSR)
	gb35114Group.POST("/enroll", a.gb35114Enroll)
	gb35114Group.POST("/verify", a.gb35114Verify)
	gb35114Group.GET("/certs", a.gb35114Certs)
	gb35114Group.POST("/certs/:id/revoke", a.gb35114Revoke)
	gb35114Group.POST("/sm3", a.gb35114SM3)

	// GA/T1400 (VIID) ingest endpoints are called by devices directly.
	if a.ga1400 != nil {
		a.ga1400.Register(r)
	}

	// Cluster heartbeat is authenticated by a shared secret, not JWT.
	api.POST("/cluster/heartbeat", a.clusterHeartbeat)

	authed.GET("/video/resources", a.listVideoResources)
	authed.GET("/video/stats", a.videoStats)
	authed.GET("/video/streams", a.liveStreams)
	authed.POST("/video/sync", a.syncChannels)

	aiGroup := authed.Group("/ai")
	aiGroup.GET("/providers", a.listProviders)
	aiGroup.POST("/providers", a.createProvider)
	aiGroup.PUT("/providers/:id", a.updateProvider)
	aiGroup.DELETE("/providers/:id", a.deleteProvider)

	aiGroup.GET("/tasks", a.listTasks)
	aiGroup.POST("/tasks", a.createTask)
	aiGroup.PUT("/tasks/:id", a.updateTask)
	aiGroup.DELETE("/tasks/:id", a.deleteTask)
	aiGroup.POST("/tasks/:id/start", a.startTask)
	aiGroup.POST("/tasks/:id/stop", a.stopTask)
	aiGroup.POST("/tasks/:id/run", a.runTaskOnce)
	aiGroup.POST("/search", a.semanticSearch)

	notifyGroup := authed.Group("/notify")
	notifyGroup.Use(a.requirePerm("notify"))
	notifyGroup.GET("/channels", a.listNotifyChannels)
	notifyGroup.POST("/channels", a.createNotifyChannel)
	notifyGroup.PUT("/channels/:id", a.updateNotifyChannel)
	notifyGroup.DELETE("/channels/:id", a.deleteNotifyChannel)
	notifyGroup.POST("/channels/:id/test", a.testNotifyChannel)
	notifyGroup.GET("/rules", a.listNotifyRules)
	notifyGroup.POST("/rules", a.createNotifyRule)
	notifyGroup.PUT("/rules/:id", a.updateNotifyRule)
	notifyGroup.DELETE("/rules/:id", a.deleteNotifyRule)

	clusterGroup := authed.Group("/cluster")
	clusterGroup.Use(a.requirePerm("cluster"))
	clusterGroup.GET("/nodes", a.clusterNodes)
	clusterGroup.GET("/stats", a.clusterStats)
	clusterGroup.GET("/config", a.clusterConfig)

	apiKeys := authed.Group("/apikeys")
	apiKeys.Use(a.requirePerm("apikey"))
	apiKeys.GET("", a.listAPIKeys)
	apiKeys.POST("", a.createAPIKey)
	apiKeys.GET("/stats", a.apiKeyStats)
	apiKeys.GET("/logs", a.apiKeyLogs)
	apiKeys.GET("/:id/logs", a.apiKeyLogs)
	apiKeys.PUT("/:id", a.updateAPIKey)
	apiKeys.DELETE("/:id", a.deleteAPIKey)

	// Open API documentation (public, no auth required).
	api.GET("/openapi.json", a.openapiSpec)

	// Open API for third parties / APP, authenticated by an API key.
	open := api.Group("/open")
	open.GET("/devices", a.apiKeyRequired("read"), a.listDevices)
	open.GET("/channels", a.apiKeyRequired("read"), a.listChannels)
	open.GET("/channels/:id/play-urls", a.apiKeyRequired("read"), a.channelPlayURLs)
	open.GET("/resources", a.apiKeyRequired("read"), a.listVideoResources)
	open.GET("/events", a.apiKeyRequired("read"), a.listEvents)
	open.POST("/events", a.apiKeyRequired("write"), a.ingestEvent)

	events := authed.Group("/events")
	events.GET("", a.listEvents)
	events.GET("/stats", a.eventStats)
	events.POST("/ingest", a.ingestEvent)

	authed.GET("/system/info", a.systemInfo)

	// Snapshot images are served without auth so they can be embedded in <img>.
	if a.snaps != nil {
		r.Static("/snapshots", a.snaps.Dir())
	}

	// Optionally serve a built frontend from web/dist.
	a.mountStatic(r)
	return r
}

// mountStatic serves the SPA when a build exists on disk.
func (a *App) mountStatic(r *gin.Engine) {
	dist := os.Getenv("EASYAVR_WEB_DIST")
	if dist == "" {
		dist = "../web/dist"
	}
	if _, err := os.Stat(dist); err != nil {
		return
	}
	r.Static("/assets", dist+"/assets")
	r.StaticFile("/favicon.ico", dist+"/favicon.ico")
	r.NoRoute(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
			return
		}
		c.File(dist + "/index.html")
	})
}
