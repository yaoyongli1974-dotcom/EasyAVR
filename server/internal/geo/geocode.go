// Package geo provides address/place search (geocoding) used by the electronic
// map. It prefers AMap (高德) when an API key is configured and otherwise falls
// back to the public OpenStreetMap Nominatim service.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Suggestion is one geocoding hit for the map search box.
type Suggestion struct {
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
	Source    string  `json:"source"`
}

// Geocoder resolves a free-text query to candidate coordinates.
type Geocoder struct {
	amapKey string
	client  *http.Client
}

// New builds a Geocoder. amapKey may be empty to use Nominatim only.
func New(amapKey string) *Geocoder {
	return &Geocoder{
		amapKey: strings.TrimSpace(amapKey),
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

// Provider reports the active provider name.
func (g *Geocoder) Provider() string {
	if g.amapKey != "" {
		return "amap"
	}
	return "nominatim"
}

// Search returns up to limit suggestions for the query.
func (g *Geocoder) Search(ctx context.Context, query string, limit int) ([]Suggestion, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	if g.amapKey != "" {
		if out, err := g.searchAMap(ctx, query, limit); err == nil && len(out) > 0 {
			return out, nil
		}
	}
	return g.searchNominatim(ctx, query, limit)
}

func (g *Geocoder) getJSON(ctx context.Context, rawURL, userAgent string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("geocoder status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (g *Geocoder) searchAMap(ctx context.Context, query string, limit int) ([]Suggestion, error) {
	u := fmt.Sprintf("https://restapi.amap.com/v3/place/text?key=%s&keywords=%s&offset=%d&page=1&extensions=base",
		url.QueryEscape(g.amapKey), url.QueryEscape(query), limit)
	var resp struct {
		Status string `json:"status"`
		Pois   []struct {
			Name     string `json:"name"`
			Location string `json:"location"`
			Address  string `json:"address"`
			PName    string `json:"pname"`
			CityName string `json:"cityname"`
			AdName   string `json:"adname"`
		} `json:"pois"`
	}
	if err := g.getJSON(ctx, u, "", &resp); err != nil {
		return nil, err
	}
	if resp.Status != "1" {
		return nil, fmt.Errorf("amap status %s", resp.Status)
	}
	out := make([]Suggestion, 0, len(resp.Pois))
	for _, p := range resp.Pois {
		lon, lat, ok := parseLngLat(p.Location)
		if !ok {
			continue
		}
		addr := strings.Join(nonEmpty(p.PName, p.CityName, p.AdName, p.Address), "")
		out = append(out, Suggestion{Name: p.Name, Address: addr, Longitude: lon, Latitude: lat, Source: "amap"})
	}
	return out, nil
}

func (g *Geocoder) searchNominatim(ctx context.Context, query string, limit int) ([]Suggestion, error) {
	u := fmt.Sprintf("https://nominatim.openstreetmap.org/search?q=%s&format=jsonv2&limit=%d&accept-language=zh-CN",
		url.QueryEscape(query), limit)
	var resp []struct {
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
	}
	if err := g.getJSON(ctx, u, "EasyAVR/1.0 (geocoding; contact: admin)", &resp); err != nil {
		return nil, err
	}
	out := make([]Suggestion, 0, len(resp))
	for _, r := range resp {
		lat, err1 := strconv.ParseFloat(r.Lat, 64)
		lon, err2 := strconv.ParseFloat(r.Lon, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		name := r.Name
		if name == "" {
			name = r.DisplayName
		}
		out = append(out, Suggestion{Name: name, Address: r.DisplayName, Longitude: lon, Latitude: lat, Source: "nominatim"})
	}
	return out, nil
}

func parseLngLat(s string) (float64, float64, bool) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	lon, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lat, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return lon, lat, true
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
