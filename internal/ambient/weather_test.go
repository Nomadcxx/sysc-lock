package ambient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseWeatherConfigCoords(t *testing.T) {
	lat, lon, unit, ok := ParseWeatherConfig(
		[]byte(`{"weather":{"latitude":-33.87,"longitude":151.21,"unit":"celsius"}}`))
	if !ok || lat != -33.87 || lon != 151.21 || unit != "celsius" {
		t.Fatalf("got %v %v %q %v", lat, lon, unit, ok)
	}
}

func TestParseWeatherConfigCityOnly(t *testing.T) {
	_, _, _, ok := ParseWeatherConfig([]byte(`{"weather":{"city":"Sydney"}}`))
	if ok {
		t.Fatal("city-only config must be rejected")
	}
}

func TestFetchTemp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"current":{"temperature_2m":18.4}}`)
	}))
	defer server.Close()
	temp, err := FetchTemp(server.URL, -33.87, 151.21, "celsius")
	if err != nil || temp != 18.4 {
		t.Fatalf("got %v, %v", temp, err)
	}
}

func TestFetchTempUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()
	if _, err := FetchTemp(url, 0, 0, "celsius"); err == nil {
		t.Fatal("unreachable server must error")
	}
}

func TestFetchTempOversize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"current":{"temperature_2m":18.4}%s}`, strings.Repeat(" ", weatherMaxBody))
	}))
	defer server.Close()
	if _, err := FetchTemp(server.URL, 0, 0, "celsius"); err == nil {
		t.Fatal("oversize body must error")
	}
}
