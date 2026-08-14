package webdav

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

type Mounter struct {
	driveLetter string
	url         string
	isMounted   bool
}

func NewMounter(driveLetter, url string) *Mounter {
	return &Mounter{
		driveLetter: driveLetter,
		url:         url,
	}
}

func (m *Mounter) Mount() error {
	switch runtime.GOOS {
	case "windows":
		return m.mountWindows()
	case "linux":
		return m.mountLinux()
	default:
		return fmt.Errorf("auto-mount is not supported on %s", runtime.GOOS)
	}
}

func (m *Mounter) Unmount() error {
	switch runtime.GOOS {
	case "windows":
		return m.unmountWindows()
	case "linux":
		return m.unmountLinux()
	default:
		return nil
	}
}

func (m *Mounter) mountWindows() error {
	drive := strings.TrimSuffix(m.driveLetter, ":") + ":"
	
	// Ensure Windows WebClient allows file transfers up to 4GB and disables timeouts
	regPath := `HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters`
	_ = exec.Command("reg", "add", regPath, "/v", "FileSizeLimitInBytes", "/t", "REG_DWORD", "/d", "4294967295", "/f").Run()
	_ = exec.Command("reg", "add", regPath, "/v", "SendReceiveTimeoutInSec", "/t", "REG_DWORD", "/d", "4294967295", "/f").Run()
	_ = exec.Command("reg", "add", regPath, "/v", "LocalServerTimeoutInSec", "/t", "REG_DWORD", "/d", "4294967295", "/f").Run()
	_ = exec.Command("reg", "add", regPath, "/v", "InternetServerTimeoutInSec", "/t", "REG_DWORD", "/d", "4294967295", "/f").Run()
	_ = exec.Command("reg", "add", regPath, "/v", "FileAttributesLimitInBytes", "/t", "REG_DWORD", "/d", "100000000", "/f").Run()

	// Unmount any stale drive first
	_ = exec.Command("net", "use", drive, "/delete", "/y").Run()

	cmd := exec.Command("net", "use", drive, m.url, "/persistent:no")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to mount Windows drive %s: %s (%w)", drive, string(out), err)
	}

	m.isMounted = true
	return nil
}

func (m *Mounter) unmountWindows() error {
	drive := strings.TrimSuffix(m.driveLetter, ":") + ":"
	cmd := exec.Command("net", "use", drive, "/delete", "/y")
	_ = cmd.Run()
	m.isMounted = false
	return nil
}

func (m *Mounter) mountLinux() error {
	// For Linux, suggest or execute davfs2 / gio mount
	mountPoint := m.driveLetter
	if mountPoint == "" || strings.Contains(mountPoint, ":") {
		mountPoint = "/mnt/tgdrive"
	}

	cmd := exec.Command("mount", "-t", "davfs", m.url, mountPoint)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to mount davfs on %s: %s (ensure davfs2 is installed)", mountPoint, string(out))
	}
	m.isMounted = true
	return nil
}

func (m *Mounter) unmountLinux() error {
	mountPoint := m.driveLetter
	if mountPoint == "" || strings.Contains(mountPoint, ":") {
		mountPoint = "/mnt/tgdrive"
	}
	_ = exec.Command("umount", mountPoint).Run()
	m.isMounted = false
	return nil
}

func (m *Mounter) IsMounted() bool {
	return m.isMounted
}
