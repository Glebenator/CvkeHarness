package recovery

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
)

func checkExtendedMetadata(fd int) error {
	n, err := unix.Flistxattr(fd, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("extended attributes/ACLs require a metadata-capable recovery backend")
	}
	return nil
}
