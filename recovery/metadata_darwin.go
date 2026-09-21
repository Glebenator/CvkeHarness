package recovery

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"runtime"
	"strings"
	"unsafe"
)

func checkExtendedMetadata(fd int) error {
	n, err := unix.Flistxattr(fd, nil)
	if err != nil && !errors.Is(err, unix.ENOTSUP) {
		return err
	}
	if n > 0 {
		names := make([]byte, n)
		if _, err := unix.Flistxattr(fd, names); err != nil {
			return err
		}
		for _, name := range strings.Split(string(names), "\x00") {
			// macOS adds this OS-managed provenance attribute even to newly
			// created files. It is not user configuration and cannot be restored
			// faithfully by this executor. All other attributes fail closed.
			if name != "" && name != "com.apple.provenance" {
				return fmt.Errorf("extended attribute %q requires a metadata-capable recovery backend", name)
			}
		}
	}
	// ATTR_CMN_EXTENDED_SECURITY reports ACL data. This backend does not yet
	// preserve macOS ACLs and must not silently remove them during replacement.
	var attrs unix.Attrlist
	attrs.Bitmapcount = unix.ATTR_BIT_MAP_COUNT
	attrs.Commonattr = unix.ATTR_CMN_EXTENDED_SECURITY
	buf := make([]byte, 4096)
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(buf)
	if errno != 0 {
		return fmt.Errorf("inspect file ACL: %w", errno)
	}
	// attrreference_t follows the four-byte returned buffer length; its second
	// uint32 is the ACL payload length. Zero means no extended security data.
	if len(buf) >= 12 && (buf[8] != 0 || buf[9] != 0 || buf[10] != 0 || buf[11] != 0) {
		return fmt.Errorf("macOS ACLs require a metadata-capable recovery backend")
	}
	return nil
}
