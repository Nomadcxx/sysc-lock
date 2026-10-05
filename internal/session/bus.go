package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const BusName = "org.sysc.LockSession1"
const BusPath = dbus.ObjectPath("/org/sysc/LockSession1")

type busAPI struct{ session *Session }

func (b *busAPI) GetState() (string, *dbus.Error) { return encodeState(b.session.Snapshot()), nil }
func (b *busAPI) Lock() (string, *dbus.Error) {
	v, err := b.session.Lock()
	if err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return encodeState(v), nil
}
func encodeState(v Snapshot) string { data, _ := json.Marshal(v); return string(data) }
func Serve(conn *dbus.Conn, s *Session) error {
	if err := conn.Export(&busAPI{s}, BusPath, BusName); err != nil {
		return err
	}
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("session owner already registered")
	}
	return nil
}
func Publish(conn *dbus.Conn, s *Session) error {
	return conn.Emit(BusPath, BusName+".Changed", encodeState(s.Snapshot()))
}

// ValidateNiri uses the startup session, never a first-socket heuristic.
func ValidateNiri(conn *dbus.Conn) (Identity, error) {
	id := Identity{UID: uint32(os.Getuid()), Session: os.Getenv("XDG_SESSION_ID")}
	socket := os.Getenv("NIRI_SOCKET")
	display := os.Getenv("WAYLAND_DISPLAY")
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	expected := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	if id.Session == "" || socket == "" || display == "" || filepath.Clean(runtime) != expected {
		return id, fmt.Errorf("missing Niri startup session registration")
	}
	if !filepath.IsAbs(socket) || filepath.Dir(socket) != expected {
		return id, fmt.Errorf("Niri socket outside registered runtime directory")
	}
	if !filepath.IsAbs(display) {
		display = filepath.Join(runtime, display)
	}
	if filepath.Dir(display) != expected || filepath.Base(display) == "." {
		return id, fmt.Errorf("Wayland display outside registered runtime directory")
	}
	runtimeInfo, err := os.Lstat(runtime)
	if err != nil {
		return id, err
	}
	runtimeStat, ok := runtimeInfo.Sys().(*syscall.Stat_t)
	if !ok || !runtimeInfo.IsDir() || runtimeInfo.Mode().Perm() != 0700 || runtimeStat.Uid != id.UID {
		return id, fmt.Errorf("runtime directory must be owned and private")
	}
	cred, st, err := socketPeer(socket, id.UID)
	if err != nil {
		return id, err
	}
	displayPeer, _, err := socketPeer(display, id.UID)
	if err != nil {
		return id, fmt.Errorf("Wayland display: %w", err)
	}
	if err = sameCompositorPeer(cred, displayPeer); err != nil {
		return id, err
	}
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", cred.Pid))
	if err != nil {
		return id, err
	}
	if filepath.Base(executable) != "niri" {
		return id, fmt.Errorf("registered peer is not Niri")
	}
	if err = validateCompositorStartup(cred.Pid, id.Session); err != nil {
		return id, err
	}
	id.Compositor = fmt.Sprintf("%s:%d:%d", socket, st.Dev, st.Ino)
	if conn == nil {
		return id, fmt.Errorf("logind unavailable for session validation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	manager := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1")
	var path dbus.ObjectPath
	if err := manager.CallWithContext(ctx, "org.freedesktop.login1.Manager.GetSession", 0, id.Session).Store(&path); err != nil {
		return id, err
	}
	props, err := sessionProperties(ctx, conn, path)
	if err != nil {
		return id, err
	}
	if !graphicalUser(props, id.UID) {
		return id, fmt.Errorf("registered logind session is not this UID's Wayland session")
	}
	var sessions []struct {
		ID   string
		UID  uint32
		User string
		Seat string
		Path dbus.ObjectPath
	}
	if err := manager.CallWithContext(ctx, "org.freedesktop.login1.Manager.ListSessions", 0).Store(&sessions); err != nil {
		return id, err
	}
	count := 0
	for _, entry := range sessions {
		if entry.UID != id.UID {
			continue
		}
		p, err := sessionProperties(ctx, conn, entry.Path)
		if err != nil {
			return id, err
		}
		if graphicalUser(p, id.UID) {
			count++
		}
	}
	if count != 1 {
		return id, fmt.Errorf("ambiguous graphical session registration (%d sessions)", count)
	}
	return id, nil
}

// socketPeer checks filesystem ownership and the kernel-authenticated listener.
func socketPeer(path string, uid uint32) (*unix.Ucred, *syscall.Stat_t, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || st.Uid != uid {
		return nil, nil, fmt.Errorf("invalid compositor socket ownership/type")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var cred *unix.Ucred
	var credErr error
	if err = raw.Control(func(fd uintptr) { cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return nil, nil, err
	}
	if credErr != nil || cred == nil || cred.Uid != uid || cred.Pid <= 0 {
		return nil, nil, fmt.Errorf("invalid compositor peer credentials")
	}
	return cred, st, nil
}
func sameCompositorPeer(ipc, display *unix.Ucred) error {
	if ipc == nil || display == nil || ipc.Pid <= 0 || ipc.Pid != display.Pid || ipc.Uid != display.Uid {
		return fmt.Errorf("Wayland display does not belong to the registered Niri peer")
	}
	return nil
}

const maxStartupEnvironment = 1 << 20

// Native niri.service runs in the user manager, outside logind's session scope.
// Its imported startup environment identifies the graphical session instead.
func validateCompositorStartup(pid int32, expected string) error {
	file, err := os.Open(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return fmt.Errorf("Niri startup registration unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStartupEnvironment+1))
	if err != nil {
		return fmt.Errorf("Niri startup registration unavailable")
	}
	defer clear(data)
	return validateStartupEnvironment(data, expected)
}
func validateStartupEnvironment(data []byte, expected string) error {
	if len(data) > maxStartupEnvironment || expected == "" {
		return fmt.Errorf("invalid Niri startup registration")
	}
	prefix := []byte("XDG_SESSION_ID=")
	count := 0
	for _, entry := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(entry, prefix) {
			count++
			if !bytes.Equal(entry[len(prefix):], []byte(expected)) {
				return fmt.Errorf("Niri startup belongs to another graphical session")
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("missing or ambiguous Niri startup session registration")
	}
	return nil
}

func sessionProperties(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath) (map[string]dbus.Variant, error) {
	var props map[string]dbus.Variant
	err := conn.Object("org.freedesktop.login1", path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.login1.Session").Store(&props)
	return props, err
}
func graphicalUser(props map[string]dbus.Variant, uid uint32) bool {
	var user struct {
		UID  uint32
		Path dbus.ObjectPath
	}
	if err := dbus.Store([]any{props["User"].Value()}, &user); err != nil {
		return false
	}
	return user.UID == uid && props["Type"].Value() == "wayland" && props["Class"].Value() == "user"
}
func Notify(message string) error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" || message == "" {
		return nil
	}
	if strings.HasPrefix(path, "@") {
		path = "\x00" + path[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err = conn.Write([]byte(message))
	return err
}
