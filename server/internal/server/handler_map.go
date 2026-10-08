package server

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

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
	query := a.db.Where("longitude != 0 AND latitude != 0").Preload("Device")
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

// haversine calculates distance between two lat/lng points in meters.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters
	dLat := (lat2 - lat1) * (math.Pi / 180)
	dLon := (lon2 - lon1) * (math.Pi / 180)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
