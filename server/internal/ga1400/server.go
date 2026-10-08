// Package ga1400 implements a GA/T1400 (VIID) view-library ingest endpoint:
// devices register, heartbeat and upload structured data (face/person/vehicle)
// or alarms as XML, which the platform stores in the AI Event Center.
package ga1400

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
)

const maxBody = 1 << 20 // 1 MiB

// Handler serves GA/T1400 VIID endpoints.
type Handler struct {
	db   *gorm.DB
	cfg  config.GA1400Config
	sink EventSink
}

// EventSink receives ingested events for notification/indexing.
type EventSink interface {
	OnEvent(model.AIEvent)
}

// SetSink attaches an event sink.
func (h *Handler) SetSink(sink EventSink) { h.sink = sink }

func NewHandler(db *gorm.DB, cfg config.GA1400Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

// Register mounts the VIID routes on the engine (devices call these directly,
// so they are not behind the JWT middleware; configure credentials instead).
func (h *Handler) Register(r *gin.Engine) {
	g := r.Group("/VIID")
	g.POST("/System/Register", h.register)
	g.POST("/System/UnRegister", h.ok)
	g.POST("/System/Keepalive", h.keepalive)
	g.POST("/Face", h.structured("face"))
	g.POST("/Person", h.structured("person"))
	g.POST("/MotorVehicle", h.structured("motor_vehicle"))
	g.POST("/NonMotorVehicle", h.structured("non_motor_vehicle"))
	g.POST("/Notification", h.notification)
	g.POST("/SubscribeNotifications", h.ok)
	g.PUT("/SubscribeNotifications", h.ok)
}

func (h *Handler) register(c *gin.Context) {
	body := readBody(c)
	if id := firstTag(body, "DeviceID"); id != "" {
		h.db.Model(&model.GBWhiteList{}).Where("device_id = ?", id).
			Updates(map[string]any{})
	}
	h.status(c, 0, "register ok")
}

func (h *Handler) keepalive(c *gin.Context) {
	h.status(c, 0, "keepalive ok")
}

func (h *Handler) ok(c *gin.Context) { h.status(c, 0, "ok") }

func (h *Handler) structured(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body := readBody(c)
		items := strings.Count(body, "<DeviceID>")
		summary := fmt.Sprintf("GA/T1400 结构化数据: %s (%d 条)", kind, items)
		h.ingest(c, kind, "info", summary, body)
	}
}

func (h *Handler) notification(c *gin.Context) {
	body := readBody(c)
	level := "warning"
	if strings.Contains(body, "<AlarmLevel>1") || strings.Contains(strings.ToLower(body), "urgent") {
		level = "critical"
	}
	id := firstTag(body, "DeviceID")
	summary := "GA/T1400 告警"
	if d := firstTag(body, "AlarmDescription"); d != "" {
		summary = d
	}
	ev := model.AIEvent{
		Kind: "ga1400", EventType: "ga1400_alarm", Level: level,
		Summary: summary, Payload: truncate(body, 64*1024),
		OccurredAt: time.Now(),
	}
	if id != "" {
		var ch model.Channel
		if h.db.Where("gb_channel_id = ? OR gb_device_id = ?", id, id).First(&ch).Error == nil {
			ev.ChannelID = ch.ID
		}
	}
	h.db.Create(&ev)
	if h.sink != nil {
		h.sink.OnEvent(ev)
	}
	h.status(c, 0, "notification ok")
}

func (h *Handler) ingest(c *gin.Context, eventType, level, summary, body string) {
	ev := model.AIEvent{
		Kind: "ga1400", EventType: "ga1400_" + eventType, Level: level,
		Summary: summary, Payload: truncate(body, 64*1024), OccurredAt: time.Now(),
	}
	h.db.Create(&ev)
	if h.sink != nil {
		h.sink.OnEvent(ev)
	}
	h.status(c, 0, "ok")
}

// status writes a GA/T1400 ResponseStatusList document.
func (h *Handler) status(c *gin.Context, code int, msg string) {
	resp := responseStatusList{
		Status: responseStatus{
			RequestURL:   c.Request.URL.String(),
			StatusCode:   code,
			StatusString: msg,
		},
	}
	out, err := xml.Marshal(resp)
	if err != nil {
		c.String(http.StatusInternalServerError, "xml error")
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", append([]byte(xml.Header), out...))
}

type responseStatusList struct {
	XMLName xml.Name       `xml:"ResponseStatusList"`
	Status  responseStatus `xml:"ResponseStatus"`
}

type responseStatus struct {
	RequestURL   string `xml:"RequestURL"`
	StatusCode   int    `xml:"StatusCode"`
	StatusString string `xml:"StatusString"`
}

func readBody(c *gin.Context) string {
	raw, _ := io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
	return string(raw)
}

// firstTag returns the text content of the first <name>...</name> occurrence.
func firstTag(body, name string) string {
	open := "<" + name + ">"
	start := strings.Index(body, open)
	if start < 0 {
		return ""
	}
	rest := body[start+len(open):]
	if end := strings.Index(rest, "</"+name+">"); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return ""
}

// rootElement returns the first XML element name.
func rootElement(body string) string {
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
