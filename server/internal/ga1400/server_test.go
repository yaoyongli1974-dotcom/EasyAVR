package ga1400

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

func TestVIIDNotificationIngest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	r := gin.New()
	NewHandler(db, config.GA1400Config{Enabled: true}).Register(r)

	body := `<?xml version="1.0"?><Notification><DeviceID>34020000001320000001</DeviceID>` +
		`<AlarmLevel>1</AlarmLevel><AlarmDescription>区域入侵</AlarmDescription></Notification>`
	req := httptest.NewRequest(http.MethodPost, "/VIID/Notification", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ResponseStatusList") {
		t.Fatalf("unexpected status %d body %s", w.Code, w.Body.String())
	}
	var count int64
	db.Model(&model.AIEvent{}).Where("kind = ?", "ga1400").Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 ga1400 event, got %d", count)
	}
	var ev model.AIEvent
	db.First(&ev)
	if ev.Level != "critical" {
		t.Fatalf("expected critical level from AlarmLevel=1, got %s", ev.Level)
	}
}

func TestVIIDStructuredIngest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _ := store.Open(filepath.Join(t.TempDir(), "g2.db"))
	r := gin.New()
	NewHandler(db, config.GA1400Config{Enabled: true}).Register(r)

	body := `<FaceList Num="1"><Face><DeviceID>D1</DeviceID></Face></FaceList>`
	req := httptest.NewRequest(http.MethodPost, "/VIID/Face", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var ev model.AIEvent
	if err := db.Where("event_type = ?", "ga1400_face").First(&ev).Error; err != nil {
		t.Fatalf("face event not stored: %v", err)
	}
}
