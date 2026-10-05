package ambient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	openMeteoURL    = "https://api.open-meteo.com/v1/forecast"
	weatherTimeout  = 2 * time.Second
	weatherMaxBody  = 64 << 10
	weatherCacheTTL = 15 * time.Minute
)

// ParseWeatherConfig reads the sysc-shell config's weather block. Only
// coordinates are accepted; a city name alone can't drive Open-Meteo and is
// rejected. Unit defaults to celsius.
func ParseWeatherConfig(data []byte) (lat, lon float64, unit string, ok bool) {
	var cfg struct {
		Weather struct {
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
			Unit      string   `json:"unit"`
		} `json:"weather"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, 0, "", false
	}
	w := cfg.Weather
	if w.Latitude == nil || w.Longitude == nil {
		return 0, 0, "", false
	}
	unit = w.Unit
	if unit == "" {
		unit = "celsius"
	}
	return *w.Latitude, *w.Longitude, unit, true
}

// FetchTemp reads the current temperature from an Open-Meteo-compatible
// endpoint. The whole exchange is capped at 2 seconds and 64 KiB.
func FetchTemp(base string, lat, lon float64, unit string) (float64, error) {
	url := fmt.Sprintf("%s?latitude=%f&longitude=%f&current=temperature_2m", base, lat, lon)
	if strings.EqualFold(unit, "fahrenheit") {
		url += "&temperature_unit=fahrenheit"
	}
	client := &http.Client{Timeout: weatherTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("weather status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, weatherMaxBody+1))
	if err != nil {
		return 0, err
	}
	if len(body) > weatherMaxBody {
		return 0, fmt.Errorf("weather body over %d bytes", weatherMaxBody)
	}
	var payload struct {
		Current struct {
			Temperature2m float64 `json:"temperature_2m"`
		} `json:"current"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, err
	}
	return payload.Current.Temperature2m, nil
}

// Weather caches one fetched temperature for 15 minutes.
type Weather struct {
	base      string
	lat, lon  float64
	unit      string
	temp      float64
	fetchedAt time.Time
	ok        bool
}

// Get returns the cached temperature when fresh, otherwise fetches. A failed
// fetch omits weather; a recent failure is not retried until the cache TTL.
func (w *Weather) Get(now time.Time) (float64, bool) {
	if !w.fetchedAt.IsZero() && now.Sub(w.fetchedAt) < weatherCacheTTL {
		return w.temp, w.ok
	}
	temp, err := FetchTemp(w.base, w.lat, w.lon, w.unit)
	w.fetchedAt = now
	if err != nil {
		w.ok = false
		return 0, false
	}
	w.temp, w.ok = temp, true
	return temp, true
}

// weatherConfigPath is $XDG_CONFIG_HOME/sysc-shell/config.json with the
// ~/.config fallback, or "" when neither base can be resolved.
func weatherConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "sysc-shell", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "sysc-shell", "config.json")
}

// newWeatherFromConfig builds the cached fetcher, or nil when no coordinates
// are configured.
func newWeatherFromConfig() *Weather {
	path := weatherConfigPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lat, lon, unit, ok := ParseWeatherConfig(data)
	if !ok {
		return nil
	}
	return &Weather{base: openMeteoURL, lat: lat, lon: lon, unit: unit}
}
