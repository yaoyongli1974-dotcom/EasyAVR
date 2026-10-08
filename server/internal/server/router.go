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
	// Public playback auth helpers (edge / ZLMediaKit on_play hook).
	api.GET("/play/verify", a.verifyPlay)
	api.POST("/zlm/on_play", a.zlmOnPlay)

	authGroup := api.Group("/auth")
	authGroup.POST("/login", a.login)

	// Everything below requires a valid JWT.
	authed := api.Group("")
	authed.Use(a.authRequired())
	// Audit log for all authenticated requests.
	authed.Use(a.auditLog())

	authed.GET("/auth/profile", a.profile)
	authed.POST("/auth/password", a.changePassword)
	authed.GET("/playback/config", a.playbackConfig)

	// User and role management is restricted to administrators.
	users := authed.Group("/users")
	users.Use(a.adminRequired())
	users.GET("", a.listUsers)
	users.POST("", a.createUser)
	users.GET("/export", a.exportUsers)
	users.POST("/import", a.importUsers)
	users.PUT("/:id", a.updateUser)
	users.DELETE("/:id", a.deleteUser)

	roles := authed.Group("/roles")
	roles.Use(a.adminRequired())
	roles.GET("", a.listRoles)
	roles.POST("", a.createRole)
	roles.PUT("/:id", a.updateRole)
	roles.DELETE("/:id", a.deleteRole)

	// Policy engine management (admin only).
	policyGroup := authed.Group("/policy")
	policyGroup.Use(a.adminRequired())
	policyGroup.GET("", a.listPolicies)
	policyGroup.POST("", a.addPolicy)
	policyGroup.DELETE("", a.removePolicy)
	policyGroup.POST("/test", a.testPolicy)
	policyGroup.GET("/user/:id/roles", a.listUserRoles)
	policyGroup.POST("/seed", a.seedDefaultPolicies)

	// Runtime platform configuration (admin only) — 手册 3.7 平台配置.
	configGroup := authed.Group("/config")
	configGroup.Use(a.adminRequired())
	configGroup.GET("/platform", a.platformConfig)
	configGroup.PUT("/platform", a.updatePlatformConfig)

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

	// Electronic map & track (手册 3.3.4/3.3.5).
	mapGroup := authed.Group("/map")
	mapGroup.Use(a.requireGroupPerm("video")) // reuse video perm for map access
	mapGroup.GET("/devices", a.listMapDevices)
	mapGroup.GET("/channels", a.listMapChannels)
	mapGroup.POST("/devices/:id/gps", a.updateDeviceGPS)
	mapGroup.POST("/channels/:id/gps", a.updateChannelGPS)
	mapGroup.GET("/tracks", a.listTracks)
	mapGroup.GET("/tracks/stats", a.getTrackStats)
	mapGroup.GET("/events", a.listMapEvents)
	mapGroup.GET("/events/stats", a.mapEventStats)
	mapGroup.GET("/geocode", a.searchGeocode)
	mapGroup.POST("/import", a.importMapCoordinates)

	d := authed.Group("/devices")
	d.Use(a.requireGroupPerm("device"))
	d.GET("", a.listDevices)
	d.POST("", a.createDevice)
	d.GET("/export", a.exportDevices)
	d.POST("/import", a.importDevices)
	d.GET("/:id", a.getDevice)
	d.PUT("/:id", a.updateDevice)
	d.DELETE("/:id", a.deleteDevice)
	d.POST("/:id/channels", a.createChannel)
	d.GET("/:id/channels", a.listDeviceChannels)
	d.GET("/:id/status-logs", a.listDeviceStatusLogs)
	d.POST("/:id/check", a.checkDevice)
	d.POST("/check", a.checkDevices)

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
	ch.GET("/export", a.exportChannels)
	ch.POST("/import", a.importChannels)
	ch.PUT("/:id", a.updateChannel)
	ch.DELETE("/:id", a.deleteChannel)
	ch.POST("/:id/start", a.startChannel)
	ch.POST("/:id/stop", a.stopChannel)
	ch.GET("/:id/play-urls", a.channelPlayURLs)
	ch.POST("/:id/play-token", a.channelPlayToken)
	ch.GET("/:id/diagnose", a.diagnoseChannel)
	ch.GET("/:id/vqd", a.diagnoseVQD)
	ch.POST("/:id/ptz", a.channelPTZ)
	ch.GET("/:id/ptz/presets", a.listPTZPresets)
	ch.POST("/:id/ptz/presets", a.savePTZPreset)
	ch.POST("/:id/ptz/presets/:preset/goto", a.gotoPTZPreset)
	ch.DELETE("/:id/ptz/presets/:preset", a.deletePTZPreset)
	ch.POST("/:id/record/start", a.startRecording)
	ch.POST("/:id/record/stop", a.stopRecording)
	ch.POST("/:id/snapshot", a.captureSnapshot)
	ch.GET("/:id/recordings", a.listChannelRecordings)
	ch.POST("/:id/recordings/sync", a.syncChannelRecordings)
	ch.GET("/:id/recording-plan", a.getRecordingPlan)
	ch.PUT("/:id/recording-plan", a.upsertRecordingPlan)
	ch.GET("/:id/traffic", a.getChannelTraffic)
	ch.GET("/:id/status-logs", a.listChannelStatusLogs)

	authed.GET("/recordings", a.listRecordings)
	authed.POST("/recordings/cleanup", a.cleanupRecordings)
	authed.PUT("/recordings/:id/mark", a.markRecording)
	authed.DELETE("/recordings/:id", a.deleteRecording)

	authed.GET("/snapshots", a.listSnapshots)
	authed.DELETE("/snapshots/:id", a.deleteSnapshot)

	// GB28181 routes with policy
	gbGroup := authed.Group("/gb")
	gbGroup.Use(a.requirePolicy("gb28181", "*", nil))
	gbGroup.GET("/devices", a.listGBDevices)
	gbGroup.GET("/config", a.gbConfig)
	gbGroup.POST("/devices/:gbid/refresh", a.refreshGBCatalog)
	gbGroup.POST("/devices/:gbid/mobile-position", a.queryGBMobilePosition)
	gbGroup.GET("/cascades", a.listCascades)
	gbGroup.POST("/cascades", a.createCascade)
	gbGroup.POST("/cascades/:id/refresh", a.refreshCascade)
	gbGroup.DELETE("/cascades/:id", a.deleteCascade)
	gbGroup.GET("/whitelist", a.listWhiteList)
	gbGroup.POST("/whitelist", a.createWhiteList)
	gbGroup.DELETE("/whitelist/:id", a.deleteWhiteList)
	gbGroup.GET("/blacklist", a.listBlackList)
	gbGroup.POST("/blacklist", a.createBlackList)
	gbGroup.DELETE("/blacklist/:id", a.deleteBlackList)

	ga1400Group := authed.Group("/ga1400")
	ga1400Group.Use(a.requirePolicy("ga1400", "*", nil))
	ga1400Group.GET("/cascades", a.listGACascades)
	ga1400Group.POST("/cascades", a.createGACascade)
	ga1400Group.POST("/cascades/:id/test", a.testGACascade)
	ga1400Group.POST("/cascades/:id/sync", a.syncGACascade)
	ga1400Group.DELETE("/cascades/:id", a.deleteGACascade)
	ga1400Group.GET("/subscriptions", a.listGASubscriptions)

	gb35114Group := authed.Group("/gb35114")
	gb35114Group.Use(a.requirePolicy("gb35114", "*", nil))
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

	// Video resource center
	videoGroup := authed.Group("/video")
	videoGroup.Use(a.requirePolicy("video", "*", nil))
	videoGroup.GET("/resources", a.listVideoResources)
	videoGroup.GET("/stats", a.videoStats)
	videoGroup.GET("/streams", a.liveStreams)
	videoGroup.POST("/sync", a.syncChannels)
	videoGroup.POST("/traffic/sync", a.collectTraffic)

	aiGroup := authed.Group("/ai")
	providers := aiGroup.Group("/providers")
	providers.Use(a.requirePolicy("ai:provider", "*", nil))
	providers.GET("", a.listProviders)
	providers.POST("", a.createProvider)
	providers.PUT("/:id", a.updateProvider)
	providers.DELETE("/:id", a.deleteProvider)

	tasks := aiGroup.Group("/tasks")
	tasks.Use(a.requirePolicy("ai:task", "*", nil))
	tasks.GET("", a.listTasks)
	tasks.POST("", a.createTask)
	tasks.PUT("/:id", a.updateTask)
	tasks.DELETE("/:id", a.deleteTask)
	tasks.POST("/:id/start", a.startTask)
	tasks.POST("/:id/stop", a.stopTask)
	tasks.POST("/:id/run", a.runTaskOnce)
	tasks.GET("/:id/schedule", a.taskSchedule)

	aiGroup.POST("/search", a.requirePolicy("ai:search", "*", nil), a.semanticSearch)

	models := aiGroup.Group("/models")
	models.Use(a.requirePolicy("ai:model", "*", nil))
	models.GET("", a.listModels)
	aiGroup.GET("/model-stats", a.requirePolicy("ai:model", "*", nil), a.modelStats)
	models.POST("", a.createModel)
	models.GET("/:id", a.getModel)
	models.PUT("/:id", a.updateModel)
	models.DELETE("/:id", a.deleteModel)
	models.GET("/:id/versions", a.listModelVersions)
	models.POST("/:id/versions", a.createModelVersion)
	models.PUT("/:id/versions/:versionId", a.updateModelVersion)
	models.POST("/:id/versions/:versionId/archive", a.archiveModelVersion)
	models.DELETE("/:id/versions/:versionId", a.deleteModelVersion)

	deployments := aiGroup.Group("/deployments")
	deployments.Use(a.requirePolicy("ai:deployment", "*", nil))
	deployments.GET("", a.listDeployments)
	deployments.POST("", a.createDeployment)
	deployments.PUT("/:id", a.updateDeployment)
	deployments.POST("/:id/activate", a.activateDeployment)
	deployments.POST("/:id/stop", a.stopDeployment)
	deployments.DELETE("/:id", a.deleteDeployment)

	// Annotation / training pipeline.
	datasets := aiGroup.Group("/datasets")
	datasets.Use(a.requirePolicy("ai:dataset", "*", nil))
	datasets.GET("", a.listDatasets)
	datasets.POST("", a.createDataset)
	datasets.GET("/:id", a.getDataset)
	datasets.PUT("/:id", a.updateDataset)
	datasets.DELETE("/:id", a.deleteDataset)
	datasets.GET("/:id/samples", a.listDatasetSamples)
	datasets.POST("/:id/samples", a.createDatasetSample)
	datasets.POST("/:id/samples/import", a.importDatasetSamples)
	datasets.PUT("/:id/samples/:sampleId", a.updateDatasetSample)
	datasets.DELETE("/:id/samples/:sampleId", a.deleteDatasetSample)

	annotations := aiGroup.Group("/annotations")
	annotations.Use(a.requirePolicy("ai:annotation", "*", nil))
	annotations.GET("", a.listAnnotationTasks)
	annotations.POST("", a.createAnnotationTask)
	annotations.PUT("/:id", a.updateAnnotationTask)
	annotations.DELETE("/:id", a.deleteAnnotationTask)
	annotations.POST("/:id/complete", a.completeAnnotationTask)

	training := aiGroup.Group("/training")
	training.Use(a.requirePolicy("ai:training", "*", nil))
	training.GET("", a.listTrainingJobs)
	training.POST("", a.createTrainingJob)
	training.PUT("/:id", a.updateTrainingJob)
	training.DELETE("/:id", a.deleteTrainingJob)
	training.POST("/:id/run", a.runTrainingJob)
	training.POST("/:id/cancel", a.cancelTrainingJob)

	aiGroup.GET("/pipeline-stats", a.requirePolicy("ai:dataset", "*", nil), a.pipelineStats)

	notifyGroup := authed.Group("/notify")
	notifyGroup.Use(a.requirePolicy("notify", "*", nil))
	notifyGroup.GET("/channels", a.listNotifyChannels)
	notifyGroup.POST("/channels", a.createNotifyChannel)
	notifyGroup.PUT("/channels/:id", a.updateNotifyChannel)
	notifyGroup.DELETE("/channels/:id", a.deleteNotifyChannel)
	notifyGroup.POST("/channels/:id/test", a.testNotifyChannel)
	notifyGroup.GET("/rules", a.listNotifyRules)
	notifyGroup.POST("/rules", a.createNotifyRule)
	notifyGroup.PUT("/rules/:id", a.updateNotifyRule)
	notifyGroup.DELETE("/rules/:id", a.deleteNotifyRule)

	// AI event tiered alert distribution (replaces traditional alert plans).
	alertGroup := authed.Group("/alert")
	alertGroup.Use(a.requirePolicy("alert", "*", nil))
	alertGroup.GET("/policies", a.listAlertPolicies)
	alertGroup.POST("/policies", a.createAlertPolicy)
	alertGroup.GET("/policies/:id", a.getAlertPolicy)
	alertGroup.PUT("/policies/:id", a.updateAlertPolicy)
	alertGroup.DELETE("/policies/:id", a.deleteAlertPolicy)
	alertGroup.POST("/policies/:id/test", a.testAlertPolicy)
	alertGroup.GET("/policies/:id/tiers", a.listAlertTiers)
	alertGroup.POST("/policies/:id/tiers", a.createAlertTier)
	alertGroup.PUT("/policies/:id/tiers/:tierId", a.updateAlertTier)
	alertGroup.DELETE("/policies/:id/tiers/:tierId", a.deleteAlertTier)
	alertGroup.GET("/deliveries", a.listAlertDeliveries)
	alertGroup.GET("/stats", a.alertStats)

	clusterGroup := authed.Group("/cluster")
	clusterGroup.Use(a.requirePolicy("cluster", "*", nil))
	clusterGroup.GET("/nodes", a.clusterNodes)
	clusterGroup.GET("/stats", a.clusterStats)
	clusterGroup.GET("/config", a.clusterConfig)

	apiKeys := authed.Group("/apikeys")
	apiKeys.Use(a.requirePolicy("apikey", "*", nil))
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
	events.Use(a.requirePolicy("event", "*", nil))
	events.GET("", a.listEvents)
	events.GET("/stats", a.eventStats)
	events.POST("/ingest", a.ingestEvent)
	events.POST("/:id/ack", a.ackEvent)

	// Audit log (admin only) - 手册 3.7.5 运维审计
	auditGroup := authed.Group("/audit")
	auditGroup.Use(a.adminRequired())
	auditGroup.GET("/logs", a.listAuditLogs)
	auditGroup.GET("/logs/:id", a.getAuditLog)
	auditGroup.GET("/logs/export", a.exportAuditLogs)

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
