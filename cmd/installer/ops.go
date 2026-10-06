package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"golang.org/x/sys/unix"
	"strings"
)

// ponytail: mirrors scripts/install checks in script order; a full path encoder
// is only needed if unit-prefixed paths ever go through here.
var prefixChars = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)

func validatePrefix(prefix string) error {
	if prefix == "/" || prefix == "" {
		return errors.New("use a user prefix")
	}
	if !strings.HasPrefix(prefix, "/") {
		return errors.New("prefix must be absolute")
	}
	if !prefixChars.MatchString(prefix) {
		return errors.New("prefix contains unsupported characters")
	}
	return nil
}

// checkCandidate mirrors the script's `[ -f candidate ] && [ -x candidate ]`.
func checkCandidate(path string) error {
	const msg = "candidate must be an executable regular file"
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New(msg)
	}
	if err := unix.Access(path, unix.X_OK); err != nil {
		return errors.New(msg)
	}
	return nil
}

// installDirs mirrors `install -d -m 0755 "$prefix/bin" "$prefix/share/systemd/user"`.
func installDirs(prefix string) error {
	for _, d := range []string{prefix + "/bin", prefix + "/share/systemd/user"} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
		if err := os.Chmod(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

// installBinary mirrors `install -m 0755 candidate $prefix/bin/sysc-lock.new`
// followed by `mv -f`; the rename is atomic like mv.
func installBinary(prefix, candidate string) error {
	data, err := os.ReadFile(candidate)
	if err != nil {
		return err
	}
	dst := prefix + "/bin/sysc-lock"
	if err := os.WriteFile(dst+".new", data, 0755); err != nil {
		return err
	}
	if err := os.Chmod(dst+".new", 0755); err != nil {
		return err
	}
	return os.Rename(dst+".new", dst)
}

// ponytail: same semantics as the script's sed `s|ExecStart=.*|...|` — replaces
// from "ExecStart=" to end of line, literal so $ in paths is safe.
var execStartRe = regexp.MustCompile(`ExecStart=[^\n]*`)

func installUnit(root, prefix string) error {
	data, err := os.ReadFile(root + "/contrib/systemd/sysc-lock-session.service")
	if err != nil {
		return err
	}
	out := execStartRe.ReplaceAllLiteral(data, []byte("ExecStart="+prefix+"/bin/sysc-lock --session"))
	return os.WriteFile(prefix+"/share/systemd/user/sysc-lock-session.service", out, 0644)
}

// ponytail: /proc scan is Linux-only, matching the target platform; a readlink
// failure (ESRCH, EACCES, ENOENT) just means the entry is not ours to see.
var procPath = "/proc"

type lockOwner struct {
	pid int
	exe string
}

func runningLockOwners(bin string) []lockOwner {
	entries, err := os.ReadDir(procPath)
	if err != nil {
		return nil
	}
	var owners []lockOwner
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(procPath + "/" + e.Name() + "/exe")
		if err != nil {
			continue
		}
		if target == bin || target == bin+" (deleted)" {
			owners = append(owners, lockOwner{pid: pid, exe: target})
		}
	}
	return owners
}

func enabledUnitLinks() ([]string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = home + "/.config"
	}
	return filepath.Glob(base + "/systemd/user/*.wants/sysc-lock-session.service")
}

func removeIfExists(path string) (bool, error) {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// findRepoRoot walks up from the CWD like scripts/install's caller: go.mod must
// declare this module and the unit template must be present.
func findRepoRoot() (string, error) {
	const marker = "module github.com/Nomadcxx/sysc-lock\n"
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(dir + "/go.mod"); err == nil && strings.Contains(string(data), marker) {
			if _, err := os.Stat(dir + "/contrib/systemd/sysc-lock-session.service"); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run the installer from inside a sysc-lock checkout")
		}
		dir = parent
	}
}
