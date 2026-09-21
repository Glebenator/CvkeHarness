//go:build linux && (amd64 || arm64)

package recovery

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Stable Linux UAPI layouts, without invoking a shell or external executable.
// https://github.com/torvalds/linux/blob/v6.18/include/uapi/linux/btrfs.h
// These ioctl encodings/layouts are restricted to Linux amd64/arm64.
type btrfsSubvolArgs struct {
	ID                                       uint64
	Name                                     [256]byte
	ParentID, DirectoryID, Generation, Flags uint64
	UUID, ParentUUID, ReceivedUUID           [16]byte
	Transactions                             [4]uint64
	Times                                    [4]struct {
		Seconds     uint64
		Nanoseconds uint32
		Padding     uint32
	}
	Reserved [8]uint64
}

func btrfsIOCTL(fd uintptr, command uintptr, ptr unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, command, uintptr(ptr))
	runtime.KeepAlive(ptr)
	if errno != 0 {
		return errno
	}
	return nil
}

func nativeSubvolume(dir *os.File) (SubvolumeIdentity, error) {
	var info btrfsSubvolArgs
	if unsafe.Sizeof(info) != 504 {
		return SubvolumeIdentity{}, fmt.Errorf("unsupported Btrfs UAPI layout")
	}
	if err := btrfsIOCTL(dir.Fd(), 0x80000000|(504<<16)|(0x94<<8)|60, unsafe.Pointer(&info)); err != nil {
		return SubvolumeIdentity{}, fmt.Errorf("Btrfs subvolume information unavailable: %w", err)
	}
	var filesystem [1024]byte
	if err := btrfsIOCTL(dir.Fd(), 0x80000000|(1024<<16)|(0x94<<8)|31, unsafe.Pointer(&filesystem[0])); err != nil {
		return SubvolumeIdentity{}, err
	}
	stat, err := dir.Stat()
	if err != nil {
		return SubvolumeIdentity{}, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(dir.Fd()), &st); err != nil {
		return SubvolumeIdentity{}, err
	}
	if !stat.IsDir() || st.Ino != 256 || info.ID <= 5 {
		return SubvolumeIdentity{}, fmt.Errorf("path must be a non-top-level Btrfs subvolume root")
	}
	// GET_SUBVOL_INFO exposes root-item flags (read-only bit 0), unlike
	// SNAP_CREATE_V2's ioctl flags (read-only bit 1).
	return SubvolumeIdentity{ID: info.ID, UUID: hex.EncodeToString(info.UUID[:]), ParentUUID: hex.EncodeToString(info.ParentUUID[:]), Filesystem: hex.EncodeToString(filesystem[16:32]), Generation: info.Generation, ReadOnly: info.Flags&1 != 0}, nil
}

func nativeFilesystem(dir *os.File) (string, error) {
	var data [1024]byte
	if err := btrfsIOCTL(dir.Fd(), 0x80000000|(1024<<16)|(0x94<<8)|31, unsafe.Pointer(&data[0])); err != nil {
		return "", fmt.Errorf("native snapshots require Btrfs: %w", err)
	}
	return hex.EncodeToString(data[16:32]), nil
}

func nativeNamespace(dir *os.File) (SnapshotNamespace, error) {
	var info btrfsSubvolArgs
	if err := btrfsIOCTL(dir.Fd(), 0x80000000|(504<<16)|(0x94<<8)|60, unsafe.Pointer(&info)); err != nil {
		return SnapshotNamespace{}, err
	}
	fs, err := nativeFilesystem(dir)
	if err != nil {
		return SnapshotNamespace{}, err
	}
	_, inode, err := dirIdentity(dir)
	return SnapshotNamespace{Filesystem: fs, RootID: info.ID, Inode: inode}, err
}

func nativeSnapshot(source, parent *os.File, name string, readOnly bool) error {
	if name == "" || len(name) > 255 || filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsRune(name, 0) {
		return fmt.Errorf("invalid snapshot name")
	}
	args := struct {
		FD             int64
		TransID, Flags uint64
		Unused         [4]uint64
		Name           [4040]byte
	}{FD: int64(source.Fd())}
	if unsafe.Sizeof(args) != 4096 {
		return fmt.Errorf("unsupported snapshot UAPI layout")
	}
	if readOnly {
		args.Flags = 2
	}
	copy(args.Name[:], name)
	if err := btrfsIOCTL(parent.Fd(), 0x40000000|(4096<<16)|(0x94<<8)|23, unsafe.Pointer(&args)); err != nil {
		return fmt.Errorf("native snapshot creation failed: %w", err)
	}
	return unix.Syncfs(int(parent.Fd()))
}

func nativeExchange(parent *os.File, name string, private *os.File, staged string) error {
	if err := unix.Renameat2(int(parent.Fd()), name, int(private.Fd()), staged, unix.RENAME_EXCHANGE); err != nil {
		return err
	}
	return unix.Syncfs(int(parent.Fd()))
}

func nativeSync(dir *os.File) error { return unix.Syncfs(int(dir.Fd())) }

func nativeSameMount(source, parent *os.File) error {
	var a, b unix.Statx_t
	if err := unix.Statx(int(source.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &a); err != nil {
		return err
	}
	if err := unix.Statx(int(parent.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &b); err != nil {
		return err
	}
	if a.Mask&unix.STATX_MNT_ID == 0 || b.Mask&unix.STATX_MNT_ID == 0 || a.Mnt_id != b.Mnt_id {
		return fmt.Errorf("snapshot target must not be a mount point")
	}
	return nil
}
