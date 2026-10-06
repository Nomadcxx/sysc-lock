package ambient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		if r.Header.Get("User-Agent") != "sysc-lock" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
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

func TestGetOmitsOnFetchFailure(t *testing.T) {
	n := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			http.Error(w, "no", 500)
			return
		}
		fmt.Fprint(w, `{"current":{"temperature_2m":18.4}}`)
	}))
	defer server.Close()
	w := &Weather{base: server.URL, unit: "celsius"}
	now := time.Now()
	if _, ok := w.Get(now); ok {
		t.Fatal("a failed fetch must omit weather")
	}
	if _, ok := w.Get(now.Add(time.Second)); ok || n != 1 {
		t.Fatalf("a recent failure must not retry, calls=%d", n)
	}
	temp, ok := w.Get(now.Add(weatherFailureRetry + time.Second))
	if !ok || temp != 18.4 || n != 2 {
		t.Fatalf("after the failure window: temp=%v ok=%v calls=%d", temp, ok, n)
	}
}
