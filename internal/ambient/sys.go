package ambient

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FS abstracts the sysfs/procfs trees so tests can feed maps.
type FS interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]string, error)
}

// ReadBattery reads the first power-supply entry of type Battery under root.
// Returns percent, charging, and whether a battery exists at all.
func ReadBattery(fs FS, root string) (int, bool, bool) {
	names, err := fs.ReadDir(root)
	if err != nil {
		return 0, false, false
	}
	for _, name := range names {
		dir := filepath.Join(root, name)
		typ, err := fs.ReadFile(filepath.Join(dir, "type"))
		if err != nil || strings.TrimSpace(string(typ)) != "Battery" {
			continue
		}
		cap, err := fs.ReadFile(filepath.Join(dir, "capacity"))
		if err != nil {
			continue
		}
		pct, err := strconv.Atoi(strings.TrimSpace(string(cap)))
		if err != nil {
			continue
		}
		charging := false
		if st, err := fs.ReadFile(filepath.Join(dir, "status")); err == nil {
			charging = strings.TrimSpace(string(st)) == "Charging"
		}
		return pct, charging, true
	}
	return 0, false, false
}

// ReadLink parses procRoute for the first non-loopback default route and
// classifies the interface wifi or wired by the presence of a wireless
// child under sysNet/<iface>.
func ReadLink(fs FS, procRoute, sysNet string) string {
	buf, err := fs.ReadFile(procRoute)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(buf), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "00000000" || fields[0] == "lo" {
			continue
		}
		entries, _ := fs.ReadDir(filepath.Join(sysNet, fields[0]))
		for _, e := range entries {
			if e == "wireless" {
				return LinkWifi
			}
		}
		return LinkWired
	}
	return ""
}

// osFS is the production FS over the real kernel trees.
type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func (osFS) ReadDir(name string) ([]string, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

const (
	sysPowerSupply = "/sys/class/power_supply"
	procNetRoute   = "/proc/net/route"
	sysClassNet    = "/sys/class/net"
)

func readBattery() (int, bool, bool) { return ReadBattery(osFS{}, sysPowerSupply) }
func readLink() string               { return ReadLink(osFS{}, procNetRoute, sysClassNet) }
