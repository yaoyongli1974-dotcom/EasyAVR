// Package server wires the HTTP API together from the platform's planes:
// device access, video middle platform, AI middle platform, resource center
// and event center.
package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/auth"
	"github.com/easyavr/easyavr/internal/cluster"
	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/device"
	"github.com/easyavr/easyavr/internal/ehome"
	"github.com/easyavr/easyavr/internal/event"
	"github.com/easyavr/easyavr/internal/ga1400"
	"github.com/easyavr/easyavr/internal/gb28181"
	"github.com/easyavr/easyavr/internal/gb35114"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/notify"
	"github.com/easyavr/easyavr/internal/openapi"
	"github.com/easyavr/easyavr/internal/recording"
	"github.com/easyavr/easyavr/internal/search"
	"github.com/easyavr/easyavr/internal/snapshot"
	"github.com/easyavr/easyavr/internal/video"
)

// App holds shared dependencies for all HTTP handlers.
type App struct {
	cfg     *config.Config
	db      *gorm.DB
	jwt     *auth.Manager
	zlm     *video.Client
	devices *device.Service
	runner  *ai.Runner
	events  *event.Service
	rec     *recording.Service
	snaps   *snapshot.Service
	gb      *gb28181.Server
	notify  *notify.Service
	search  *search.Service
	cluster *cluster.Service
	ga1400  *ga1400.Handler
	gaCas   *ga1400.CascadeService
	ehome   *ehome.Server
	gb35114 *gb35114.Service
	sink    *eventSink
	limiter *openapi.Limiter
	engine  *gin.Engine
}

// New assembles the application and its routes.
func New(cfg *config.Config, db *gorm.DB) *App {
	zlm := video.NewClient(
		cfg.ZLM.APIBase, cfg.ZLM.Secret, cfg.ZLM.MediaHost,
		cfg.ZLM.HTTPPort, cfg.ZLM.WSPort, cfg.ZLM.RTSPPort, cfg.ZLM.RTMPPort,
	)
	notifySvc := notify.NewService(db)
	searchSvc := search.NewService(db, cfg.PGVector)
	sink := &eventSink{search: searchSvc, notify: notifySvc}
	a := &App{
		cfg:     cfg,
		db:      db,
		jwt:     auth.NewManager(cfg.JWTSecret, cfg.JWTExpireH),
		zlm:     zlm,
		devices: device.NewService(db, zlm),
		runner:  ai.NewRunner(db, zlm),
		events:  event.NewService(db),
		rec:     recording.NewService(db, zlm),
		snaps:   snapshot.NewService(db, zlm, cfg.SnapshotDir),
		notify:  notifySvc,
		search:  searchSvc,
		cluster: cluster.NewService(cfg.Cluster, db),
		sink:    sink,
		limiter: openapi.NewLimiter(),
	}
	a.runner.SetSink(sink)
	if cfg.GB.Enabled {
		a.gb = gb28181.NewServer(cfg.GB, db, zlm)
		a.gb.SetSink(sink)
	}
	if cfg.GA1400.Enabled {
		a.ga1400 = ga1400.NewHandler(db, cfg.GA1400)
		a.ga1400.SetSink(sink)
		a.gaCas = ga1400.NewCascadeService(db, cfg.GA1400)
		sink.gaCas = a.gaCas
	}
	if cfg.EHOME.Enabled {
		a.ehome = ehome.NewServer(cfg.EHOME, db)
	}
	if cfg.GB35114.Enabled {
		a.gb35114 = gb35114.NewService(db, cfg.GB35114)
	}
	a.engine = a.buildRouter()
	return a
}

// Engine exposes the underlying gin engine (used in tests).
func (a *App) Engine() *gin.Engine { return a.engine }

// Run starts background schedulers and the HTTP server.
func (a *App) Run() error {
	ctx := context.Background()
	a.rec.StartScheduler(ctx, time.Minute)
	a.snaps.StartScheduler(ctx, 30*time.Second)
	a.cluster.Start(ctx)
	if a.gaCas != nil {
		a.gaCas.Start(ctx)
	}
	if a.gb != nil {
		if err := a.gb.Start(); err != nil {
			log.Printf("[easyavr] GB28181 SIP server disabled: %v", err)
		} else {
			var cascades []model.GBCascade
			a.db.Find(&cascades)
			a.gb.StartCascades(cascades)
		}
	}
	if a.ehome != nil {
		if err := a.ehome.Start(); err != nil {
			log.Printf("[easyavr] EHOME server disabled: %v", err)
		}
	}
	if a.gb != nil && a.gb35114 != nil && a.cfg.GB35114.SIPListen != "" {
		if err := a.startSecureSIP(); err != nil {
			log.Printf("[easyavr] GB35114 secure SIP disabled: %v", err)
		}
	}
	log.Printf("[easyavr] listening on %s", a.cfg.Listen)
	return a.engine.Run(a.cfg.Listen)
}

// startSecureSIP launches the GB35114 GM/T 0024 secure SIP listener using the
// platform dual SM2 certificate.
func (a *App) startSecureSIP() error {
	pair, err := a.gb35114.PlatformTLS()
	if err != nil {
		return err
	}
	pool, err := a.gb35114.CAPool()
	if err != nil {
		return err
	}
	return a.gb.StartTLS(gb28181.TLSConfig{
		Listen:      a.cfg.GB35114.SIPListen,
		SignCertPEM: pair.SignCertPEM, SignKeyPEM: pair.SignKeyPEM,
		EncCertPEM: pair.EncCertPEM, EncKeyPEM: pair.EncKeyPEM,
		CAPool: pool, RequireClientCert: a.cfg.GB35114.SIPRequireClient,
	})
}

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"code": status, "message": msg})
}

func ok(c *gin.Context, data any) {
	c.JSON(200, gin.H{"code": 0, "message": "ok", "data": data})
}

func parseUintParam(c *gin.Context, name string) (uint, bool) {
	var id uint
	if _, err := fmt.Sscanf(c.Param(name), "%d", &id); err != nil || id == 0 {
		fail(c, 400, "invalid "+name)
		return 0, false
	}
	return id, true
}

// pagination parses page/pageSize query params with safe defaults.
func pagination(c *gin.Context) (page, pageSize int) {
	page, pageSize = 1, 20
	fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &page)
	fmt.Sscanf(c.DefaultQuery("pageSize", "20"), "%d", &pageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return page, pageSize
}
