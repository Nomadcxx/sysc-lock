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
// Returns percent, the normalized power state ("" when unknown), and whether
// a battery exists at all.
func ReadBattery(fs FS, root string) (int, string, bool) {
	names, err := fs.ReadDir(root)
	if err != nil {
		return 0, "", false
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
		power := ""
		if st, err := fs.ReadFile(filepath.Join(dir, "status")); err == nil {
			power = powerState(strings.TrimSpace(string(st)))
		}
		return pct, power, true
	}
	return 0, "", false
}

func powerState(status string) string {
	switch status {
	case "Charging":
		return PowerCharging
	case "Discharging":
		return PowerDischarging
	case "Full":
		return PowerFull
	case "Not charging":
		return PowerPlugged
	}
	return ""
}

// ReadLink parses procRoute for the first non-loopback default route and
// classifies the interface wifi or wired by the presence of a wireless
// child under sysNet/<iface>.
func ReadLink(fs FS, procRoute, sysNet string) string {
	if iface := ipv4Default(fs, procRoute); iface != "" {
		return classifyLink(fs, sysNet, iface)
	}
	buf, err := fs.ReadFile(filepath.Join(filepath.Dir(procRoute), "ipv6_route"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(buf), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[9] == "lo" || fields[0] != strings.Repeat("0", 32) || fields[1] != "00" {
			continue
		}
		return classifyLink(fs, sysNet, fields[9])
	}
	return ""
}

func ipv4Default(fs FS, procRoute string) string {
	buf, err := fs.ReadFile(procRoute)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(buf), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "00000000" || fields[0] == "lo" {
			continue
		}
		return fields[0]
	}
	return ""
}

func classifyLink(fs FS, sysNet, iface string) string {
	entries, _ := fs.ReadDir(filepath.Join(sysNet, iface))
	for _, e := range entries {
		if e == "wireless" {
			return LinkWifi
		}
	}
	return LinkWired
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

func readBattery() (int, string, bool) { return ReadBattery(osFS{}, sysPowerSupply) }
func readLink() string                 { return ReadLink(osFS{}, procNetRoute, sysClassNet) }
