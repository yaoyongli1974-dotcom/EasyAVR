package server

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- map / track ----

// listMapDevices returns devices with GPS coordinates for map display.
func (a *App) listMapDevices(c *gin.Context) {
	var devices []model.Device
	query := a.db.Where("longitude != 0 AND latitude != 0")
	if groupID := c.Query("groupId"); groupID != "" {
		if id, err := strconv.ParseUint(groupID, 10, 64); err == nil {
			query = query.Where("group_id = ? OR group_id IN (SELECT id FROM device_groups WHERE path LIKE ?)",
				id, "%/"+groupID+"/%")
		}
	}
	query.Find(&devices)
	ok(c, devices)
}

// listMapChannels returns channels with GPS coordinates for map display.
func (a *App) listMapChannels(c *gin.Context) {
	var channels []model.Channel
	query := a.db.Where("longitude != 0 AND latitude != 0")
	if deviceID := c.Query("deviceId"); deviceID != "" {
		query = query.Where("device_id = ?", deviceID)
	}
	query.Find(&channels)
	ok(c, channels)
}

type gpsUpdateReq struct {
	Longitude float64    `json:"longitude"`
	Latitude  float64    `json:"latitude"`
	Altitude  float64    `json:"altitude"`
	Heading   float64    `json:"heading"`
	Speed     float64    `json:"speed"`
	GPSTime   *time.Time `json:"gpsTime"`
	Accuracy  float64    `json:"accuracy"`
	Source    string     `json:"source"` // device, channel, manual, gb28181
}

// updateDeviceGPS updates device GPS position and creates a track point.
func (a *App) updateDeviceGPS(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var req gpsUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	var d model.Device
	if err := a.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "device not found")
		return
	}
	now := time.Now()
	gpsTime := now
	if req.GPSTime != nil {
		gpsTime = *req.GPSTime
	}
	d.Longitude = req.Longitude
	d.Latitude = req.Latitude
	d.Altitude = req.Altitude
	d.Heading = req.Heading
	d.Speed = req.Speed
	d.GPSTime = &gpsTime
	if err := a.db.Save(&d).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// Create track point
	track := model.Track{
		DeviceID:  d.ID,
		Longitude: req.Longitude,
		Latitude:  req.Latitude,
		Altitude:  req.Altitude,
		Heading:   req.Heading,
		Speed:     req.Speed,
		Accuracy:  req.Accuracy,
		Source:    req.Source,
		TrackTime: gpsTime,
	}
	if track.Source == "" {
		track.Source = "manual"
	}
	a.db.Create(&track)
	ok(c, d)
}

// updateChannelGPS updates channel GPS position and creates a track point.
func (a *App) updateChannelGPS(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var req gpsUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	now := time.Now()
	gpsTime := now
	if req.GPSTime != nil {
		gpsTime = *req.GPSTime
	}
	ch.Longitude = req.Longitude
	ch.Latitude = req.Latitude
	ch.Altitude = req.Altitude
	ch.Heading = req.Heading
	ch.Speed = req.Speed
	ch.GPSTime = &gpsTime
	if err := a.db.Save(&ch).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	track := model.Track{
		DeviceID:  ch.DeviceID,
		ChannelID: ch.ID,
		Longitude: req.Longitude,
		Latitude:  req.Latitude,
		Altitude:  req.Altitude,
		Heading:   req.Heading,
		Speed:     req.Speed,
		Accuracy:  req.Accuracy,
		Source:    req.Source,
		TrackTime: gpsTime,
	}
	if track.Source == "" {
		track.Source = "manual"
	}
	a.db.Create(&track)
	ok(c, ch)
}

// listTracks returns track points for a device or channel within a time range.
func (a *App) listTracks(c *gin.Context) {
	deviceID := c.Query("deviceId")
	channelID := c.Query("channelId")
	startStr := c.Query("start")
	endStr := c.Query("end")
	limit := 1000
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 10000 {
			limit = v
		}
	}

	query := a.db.Model(&model.Track{}).Order("track_time ASC").Limit(limit)

	if deviceID != "" {
		if id, err := strconv.ParseUint(deviceID, 10, 64); err == nil {
			query = query.Where("device_id = ?", id)
		}
	}
	if channelID != "" {
		if id, err := strconv.ParseUint(channelID, 10, 64); err == nil {
			query = query.Where("channel_id = ?", id)
		}
	}
	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			query = query.Where("track_time >= ?", t)
		}
	}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			query = query.Where("track_time <= ?", t)
		}
	}

	var tracks []model.Track
	query.Find(&tracks)
	ok(c, tracks)
}

// getTrackStats returns statistics for a track (distance, duration, max speed, etc.).
func (a *App) getTrackStats(c *gin.Context) {
	deviceID := c.Query("deviceId")
	channelID := c.Query("channelId")
	startStr := c.Query("start")
	endStr := c.Query("end")

	query := a.db.Model(&model.Track{})

	if deviceID != "" {
		if id, err := strconv.ParseUint(deviceID, 10, 64); err == nil {
			query = query.Where("device_id = ?", id)
		}
	}
	if channelID != "" {
		if id, err := strconv.ParseUint(channelID, 10, 64); err == nil {
			query = query.Where("channel_id = ?", id)
		}
	}
	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			query = query.Where("track_time >= ?", t)
		}
	}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			query = query.Where("track_time <= ?", t)
		}
	}

	var count int64
	query.Count(&count)

	var tracks []model.Track
	query.Order("track_time ASC").Find(&tracks)

	var totalDistance float64
	var maxSpeed float64
	var minAlt, maxAlt float64
	first := true
	for i := 1; i < len(tracks); i++ {
		p1 := tracks[i-1]
		p2 := tracks[i]
		// Haversine distance
		d := haversine(p1.Latitude, p1.Longitude, p2.Latitude, p2.Longitude)
		totalDistance += d
		if p2.Speed > maxSpeed {
			maxSpeed = p2.Speed
		}
		if first || p2.Altitude < minAlt {
			minAlt = p2.Altitude
		}
		if first || p2.Altitude > maxAlt {
			maxAlt = p2.Altitude
		}
		first = false
	}

	var duration int64
	if len(tracks) >= 2 {
		duration = int64(tracks[len(tracks)-1].TrackTime.Sub(tracks[0].TrackTime).Seconds())
	}

	ok(c, gin.H{
		"pointCount":    count,
		"totalDistance": totalDistance, // meters
		"maxSpeed":      maxSpeed,      // km/h
		"minAltitude":   minAlt,
		"maxAltitude":   maxAlt,
		"durationSec":   duration,
	})
}

// ---- AI event map ----

// geoCoord is a channel GPS position used to place events on the map.
type geoCoord struct {
	Name      string
	Longitude float64
	Latitude  float64
}

// channelCoords indexes channels that have a GPS position by channel id.
func (a *App) channelCoords() map[uint]geoCoord {
	var channels []model.Channel
	a.db.Select("id, name, longitude, latitude").
		Where("longitude != 0 AND latitude != 0").Find(&channels)
	coords := make(map[uint]geoCoord, len(channels))
	for _, ch := range channels {
		coords[ch.ID] = geoCoord{Name: ch.Name, Longitude: ch.Longitude, Latitude: ch.Latitude}
	}
	return coords
}

// mapEventsQuery builds the filtered AI event query shared by list/stats.
func mapEventsQuery(db *gorm.DB, c *gin.Context) *gorm.DB {
	q := db.Model(&model.AIEvent{})
	if v := c.Query("kind"); v != "" {
		q = q.Where("kind = ?", v)
	}
	if v := c.Query("level"); v != "" {
		q = q.Where("level = ?", v)
	}
	if v := c.Query("eventType"); v != "" {
		q = q.Where("event_type = ?", v)
	}
	if v := c.Query("channelId"); v != "" {
		if id, err := strconv.ParseUint(v, 10, 64); err == nil {
			q = q.Where("channel_id = ?", id)
		}
	}
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("summary LIKE ? OR event_type LIKE ?", like, like)
	}
	if from := parseTime(c.Query("from")); from != nil {
		q = q.Where("occurred_at >= ?", *from)
	}
	if to := parseTime(c.Query("to")); to != nil {
		q = q.Where("occurred_at <= ?", *to)
	}
	return q
}

type mapEventItem struct {
	model.AIEvent
	ChannelName string  `json:"channelName"`
	Longitude   float64 `json:"longitude"`
	Latitude    float64 `json:"latitude"`
}

// listMapEvents returns AI events placed on the map via their channel GPS.
func (a *App) listMapEvents(c *gin.Context) {
	coords := a.channelCoords()
	if len(coords) == 0 {
		ok(c, []mapEventItem{})
		return
	}
	limit := 500
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 5000 {
			limit = v
		}
	}
	var events []model.AIEvent
	mapEventsQuery(a.db, c).Order("occurred_at DESC").Limit(limit).Find(&events)

	items := make([]mapEventItem, 0, len(events))
	for _, e := range events {
		cd, found := coords[e.ChannelID]
		if !found {
			continue
		}
		items = append(items, mapEventItem{AIEvent: e, ChannelName: cd.Name, Longitude: cd.Longitude, Latitude: cd.Latitude})
	}
	ok(c, items)
}

// mapEventStats aggregates located AI events by type and level.
func (a *App) mapEventStats(c *gin.Context) {
	coords := a.channelCoords()
	var events []model.AIEvent
	mapEventsQuery(a.db, c).Find(&events)

	byType := map[string]int{}
	byLevel := map[string]int{}
	located := 0
	for _, e := range events {
		if _, found := coords[e.ChannelID]; !found {
			continue
		}
		located++
		byType[e.EventType]++
		byLevel[e.Level]++
	}
	ok(c, gin.H{
		"total":   len(events),
		"located": located,
		"byType":  byType,
		"byLevel": byLevel,
	})
}

// searchGeocode resolves a free-text address/place query to coordinates for the
// map search box (autocomplete). Uses AMap when configured, else Nominatim.
func (a *App) searchGeocode(c *gin.Context) {
	q := c.Query("q")
	limit := 8
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			limit = n
		}
	}
	items, err := a.geo.Search(c.Request.Context(), q, limit)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"provider": a.geo.Provider(), "items": items})
}

// importMapCoordinates bulk-sets device/channel coordinates from CSV lines of
// "name,longitude,latitude[,altitude]". Existing rows are matched by name.
func (a *App) importMapCoordinates(c *gin.Context) {
	var req struct {
		Target string `json:"target"` // device | channel
		CSV    string `json:"csv"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CSV) == "" {
		fail(c, http.StatusBadRequest, "csv is required")
		return
	}
	if req.Target != "channel" {
		req.Target = "device"
	}
	now := time.Now()
	updated, skipped := 0, 0
	for _, line := range strings.Split(req.CSV, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			skipped++
			continue
		}
		name := strings.TrimSpace(fields[0])
		lon, e1 := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		lat, e2 := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
		if name == "" || e1 != nil || e2 != nil {
			skipped++
			continue
		}
		var alt float64
		if len(fields) >= 4 {
			alt, _ = strconv.ParseFloat(strings.TrimSpace(fields[3]), 64)
		}
		if req.Target == "channel" {
			var ch model.Channel
			if err := a.db.Where("name = ?", name).First(&ch).Error; err != nil {
				skipped++
				continue
			}
			a.db.Model(&ch).Updates(map[string]any{"longitude": lon, "latitude": lat, "altitude": alt, "gps_time": &now})
			a.db.Create(&model.Track{DeviceID: ch.DeviceID, ChannelID: ch.ID, Longitude: lon, Latitude: lat, Altitude: alt, Source: "import", TrackTime: now})
			updated++
		} else {
			var d model.Device
			if err := a.db.Where("name = ?", name).First(&d).Error; err != nil {
				skipped++
				continue
			}
			a.db.Model(&d).Updates(map[string]any{"longitude": lon, "latitude": lat, "altitude": alt, "gps_time": &now})
			a.db.Create(&model.Track{DeviceID: d.ID, Longitude: lon, Latitude: lat, Altitude: alt, Source: "import", TrackTime: now})
			updated++
		}
	}
	ok(c, gin.H{"updated": updated, "skipped": skipped})
}

// haversine calculates distance between two lat/lng points in meters.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters
	dLat := (lat2 - lat1) * (math.Pi / 180)
	dLon := (lon2 - lon1) * (math.Pi / 180)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
