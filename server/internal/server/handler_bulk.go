package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

// ---- CSV bulk import/export (手册 3.2.3 / 3.4) ----

func writeCSV(c *gin.Context, filename string, header []string, rows [][]string) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM so Excel renders Chinese
	w := csv.NewWriter(&buf)
	_ = w.Write(header)
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// readCSV accepts either a raw text/csv body or a JSON object {"csv": "..."}.
func readCSV(c *gin.Context) ([][]string, error) {
	raw, err := c.GetRawData()
	if err != nil {
		return nil, err
	}
	text := string(raw)
	ct := c.GetHeader("Content-Type")
	if strings.Contains(ct, "application/json") || strings.HasPrefix(strings.TrimSpace(text), "{") {
		var body struct {
			CSV string `json:"csv"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		text = body.CSV
	}
	text = strings.TrimPrefix(text, "\xEF\xBB\xBF")
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	return r.ReadAll()
}

func headerIndex(header []string) map[string]int {
	m := map[string]int{}
	for i, h := range header {
		m[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return m
}

func cell(row []string, idx map[string]int, key string) string {
	if i, ok := idx[key]; ok && i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

// ---- devices ----

func (a *App) exportDevices(c *gin.Context) {
	var devices []model.Device
	a.db.Order("id").Find(&devices)
	rows := make([][]string, 0, len(devices))
	for _, d := range devices {
		rows = append(rows, []string{
			d.Name, d.Protocol, d.AccessMode, d.Manufacturer, d.IP,
			strconv.Itoa(d.Port), d.Username, "",
		})
	}
	writeCSV(c, "devices.csv", []string{"name", "protocol", "accessMode", "manufacturer", "ip", "port", "username", "password"}, rows)
}

func (a *App) importDevices(c *gin.Context) {
	records, err := readCSV(c)
	if err != nil || len(records) < 2 {
		fail(c, http.StatusBadRequest, "CSV must contain a header and at least one row")
		return
	}
	idx := headerIndex(records[0])
	created, skipped := 0, 0
	for _, row := range records[1:] {
		name := cell(row, idx, "name")
		if name == "" {
			skipped++
			continue
		}
		port, _ := strconv.Atoi(cell(row, idx, "port"))
		proto := cell(row, idx, "protocol")
		if proto == "" {
			proto = "rtsp"
		}
		access := cell(row, idx, "accessmode")
		if access == "" {
			access = "pull"
		}
		dev := model.Device{
			Name: name, Protocol: proto, AccessMode: access,
			Manufacturer: cell(row, idx, "manufacturer"),
			IP:           cell(row, idx, "ip"), Port: port,
			Username: cell(row, idx, "username"), Password: cell(row, idx, "password"),
			Status: "offline",
		}
		if dev.IP == "" {
			skipped++
			continue
		}
		if err := a.db.Create(&dev).Error; err != nil {
			skipped++
			continue
		}
		created++
	}
	ok(c, gin.H{"created": created, "skipped": skipped})
}

// ---- channels ----

func (a *App) exportChannels(c *gin.Context) {
	var channels []model.Channel
	a.db.Order("id").Find(&channels)
	devNames := map[uint]string{}
	devIPs := map[uint]string{}
	var devs []model.Device
	a.db.Find(&devs)
	for _, d := range devs {
		devNames[d.ID] = d.Name
		devIPs[d.ID] = d.IP
	}
	rows := make([][]string, 0, len(channels))
	for _, chn := range channels {
		rows = append(rows, []string{
			devNames[chn.DeviceID], devIPs[chn.DeviceID], chn.Name,
			chn.StreamType, chn.SourceURL,
		})
	}
	writeCSV(c, "channels.csv", []string{"deviceName", "deviceIp", "name", "streamType", "sourceUrl"}, rows)
}

func (a *App) importChannels(c *gin.Context) {
	records, err := readCSV(c)
	if err != nil || len(records) < 2 {
		fail(c, http.StatusBadRequest, "CSV must contain a header and at least one row")
		return
	}
	idx := headerIndex(records[0])
	created, skipped := 0, 0
	for _, row := range records[1:] {
		name := cell(row, idx, "name")
		devName := cell(row, idx, "devicename")
		devIP := cell(row, idx, "deviceip")
		if name == "" || (devName == "" && devIP == "") {
			skipped++
			continue
		}
		tx := a.db.Model(&model.Device{})
		if devName != "" {
			tx = tx.Where("name = ?", devName)
		}
		if devIP != "" {
			tx = tx.Where("ip = ?", devIP)
		}
		var dev model.Device
		if err := tx.First(&dev).Error; err != nil {
			skipped++
			continue
		}
		st := cell(row, idx, "streamtype")
		if st == "" {
			st = "main"
		}
		chn := model.Channel{
			DeviceID: dev.ID, Name: name, StreamType: st,
			SourceURL: cell(row, idx, "sourceurl"), StreamKey: newStreamKey(), Enabled: true,
		}
		if err := a.db.Create(&chn).Error; err != nil {
			skipped++
			continue
		}
		created++
	}
	ok(c, gin.H{"created": created, "skipped": skipped})
}

// ---- users ----

func (a *App) exportUsers(c *gin.Context) {
	var users []model.User
	a.db.Order("id").Find(&users)
	rows := make([][]string, 0, len(users))
	for _, u := range users {
		rows = append(rows, []string{u.Username, u.Nickname, u.Role, strconv.FormatBool(u.Enabled)})
	}
	writeCSV(c, "users.csv", []string{"username", "nickname", "role", "enabled"}, rows)
}

func (a *App) importUsers(c *gin.Context) {
	records, err := readCSV(c)
	if err != nil || len(records) < 2 {
		fail(c, http.StatusBadRequest, "CSV must contain a header and at least one row")
		return
	}
	idx := headerIndex(records[0])
	created, skipped := 0, 0
	for _, row := range records[1:] {
		username := cell(row, idx, "username")
		if username == "" {
			skipped++
			continue
		}
		var count int64
		a.db.Model(&model.User{}).Where("username = ?", username).Count(&count)
		if count > 0 {
			skipped++
			continue
		}
		role := cell(row, idx, "role")
		if role == "" {
			role = "viewer"
		}
		if !a.roleExists(role) {
			skipped++
			continue
		}
		password := cell(row, idx, "password")
		if password == "" {
			password = "EasyAVR@123"
		}
		hash, err := store.HashPassword(password)
		if err != nil {
			skipped++
			continue
		}
		enabled := true
		if v := cell(row, idx, "enabled"); v != "" {
			enabled, _ = strconv.ParseBool(v)
		}
		u := model.User{Username: username, Nickname: cell(row, idx, "nickname"), PasswordHash: hash, Role: role, Enabled: enabled}
		if err := a.db.Create(&u).Error; err != nil {
			skipped++
			continue
		}
		created++
	}
	ok(c, gin.H{"created": created, "skipped": skipped})
}
