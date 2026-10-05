package ambient

import (
	"os"
	"testing"
)

type mapFS struct {
	files map[string]string
	dirs  map[string][]string
}

func (m mapFS) ReadFile(name string) ([]byte, error) {
	s, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(s), nil
}

func (m mapFS) ReadDir(name string) ([]string, error) {
	d, ok := m.dirs[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return d, nil
}

func TestReadBatteryFindsFirstBattery(t *testing.T) {
	fs := mapFS{
		files: map[string]string{
			"/ps/ADP0/type":     "Mains\n",
			"/ps/BAT0/type":     "Battery\n",
			"/ps/BAT0/capacity": "82\n",
			"/ps/BAT0/status":   "Charging\n",
		},
		dirs: map[string][]string{"/ps": {"ADP0", "BAT0"}},
	}
	pct, charging, ok := ReadBattery(fs, "/ps")
	if !ok || pct != 82 || !charging {
		t.Fatalf("got %d %v %v", pct, charging, ok)
	}
}

func TestReadBatterySkipsMains(t *testing.T) {
	fs := mapFS{
		files: map[string]string{"/ps/ADP0/type": "Mains\n"},
		dirs:  map[string][]string{"/ps": {"ADP0"}},
	}
	if _, _, ok := ReadBattery(fs, "/ps"); ok {
		t.Fatal("mains-only must read as no battery")
	}
}

func TestReadBatteryNoDir(t *testing.T) {
	if _, _, ok := ReadBattery(mapFS{}, "/ps"); ok {
		t.Fatal("missing dir must read as no battery")
	}
}

const routeHeader = "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"

func TestReadLinkWireless(t *testing.T) {
	fs := mapFS{
		files: map[string]string{
			"/route": routeHeader + "wlan0\t00000000\t0102A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n",
		},
		dirs: map[string][]string{"/net/wlan0": {"wireless", "device"}},
	}
	if got := ReadLink(fs, "/route", "/net"); got != LinkWifi {
		t.Fatalf("got %q", got)
	}
}

func TestReadLinkWired(t *testing.T) {
	fs := mapFS{
		files: map[string]string{
			"/route": routeHeader + "eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
		},
		dirs: map[string][]string{"/net/eth0": {"device"}},
	}
	if got := ReadLink(fs, "/route", "/net"); got != LinkWired {
		t.Fatalf("got %q", got)
	}
}

func TestReadLinkLoopbackOnly(t *testing.T) {
	fs := mapFS{
		files: map[string]string{
			"/route": routeHeader + "lo\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n",
		},
		dirs: map[string][]string{"/net/lo": {"device"}},
	}
	if got := ReadLink(fs, "/route", "/net"); got != "" {
		t.Fatalf("got %q", got)
	}
}
