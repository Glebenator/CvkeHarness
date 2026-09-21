//go:build linux || darwin

package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"golang.org/x/sys/unix"
)

type SnapshotInventory struct {
	Entries      int    `json:"entries"`
	LogicalBytes int64  `json:"logical_bytes"`
	Digest       string `json:"digest"`
}

// Inventory stays bounded and never follows a symlink or crosses a nested
// subvolume/mount. The first backend supports regular files/directories with
// the same explicit metadata boundary as file recovery. Native snapshots are
// atomic, but this precondition inventory is not application quiescence.
func snapshotInventory(ctx context.Context, root *os.File, cfg SnapshotTarget) (SnapshotInventory, error) {
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return SnapshotInventory{}, err
	}
	owned := os.NewFile(uintptr(fd), root.Name())
	defer owned.Close()
	root = owned
	rootDevice, _, err := dirIdentity(root)
	if err != nil {
		return SnapshotInventory{}, err
	}
	type node struct {
		Path              string
		Directory         bool
		Mode, UID, GID    uint32
		ModifiedNS, Bytes int64
		Hash              string
	}
	var nodes []node
	var total int64
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, relative string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 || len(nodes) >= cfg.MaxEntries {
			return fmt.Errorf("snapshot entry/depth budget exceeded")
		}
		i, err := dir.Stat()
		if err != nil {
			return err
		}
		st := i.Sys().(*syscall.Stat_t)
		if uint64(st.Dev) != rootDevice || relative != "." && st.Ino == 256 {
			return fmt.Errorf("nested subvolume or mount is outside snapshot coverage")
		}
		if relative != "." {
			if err := nativeSameMount(dir, root); err != nil {
				return fmt.Errorf("nested mount is outside snapshot coverage: %w", err)
			}
		}
		if i.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("snapshot directory has unsupported special mode bits")
		}
		if err = checkExtendedMetadata(int(dir.Fd())); err != nil {
			return err
		}
		nodes = append(nodes, node{Path: relative, Directory: true, Mode: uint32(i.Mode().Perm()), UID: st.Uid, GID: st.Gid, ModifiedNS: i.ModTime().UnixNano()})
		for {
			entries, readErr := dir.ReadDir(128)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if len(nodes) >= cfg.MaxEntries {
					return fmt.Errorf("snapshot entry budget exceeded")
				}
				name := entry.Name()
				path := filepath.Join(relative, name)
				if err := protectedSnapshotEntry(filepath.Join(cfg.Path, path)); err != nil {
					return err
				}
				if entry.IsDir() {
					fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
					if err != nil {
						return err
					}
					child := os.NewFile(uintptr(fd), name)
					err = walk(child, path, depth+1)
					child.Close()
					if err != nil {
						return err
					}
				} else {
					// Same-filesystem file bind mounts retain st_dev; compare the
					// kernel mount identity as well as the regular-file fingerprint.
					fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
					if err != nil {
						return err
					}
					file := os.NewFile(uintptr(fd), name)
					err = nativeSameMount(file, root)
					file.Close()
					if err != nil {
						return fmt.Errorf("mounted file is outside snapshot coverage: %w", err)
					}
					fp, _, err := readRegular(dir, name, min(cfg.MaxLogicalBytes-total, 64<<20))
					if err != nil {
						return err
					}
					if !fp.Exists || fp.Device != rootDevice {
						return fmt.Errorf("snapshot file changed or crosses a mount")
					}
					total += fp.Bytes
					nodes = append(nodes, node{Path: path, Mode: fp.Mode, UID: fp.UID, GID: fp.GID, ModifiedNS: fp.ModifiedNS, Bytes: fp.Bytes, Hash: fp.SHA256})
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		return nil
	}
	if err = walk(root, ".", 0); err != nil {
		return SnapshotInventory{}, err
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	b, err := json.Marshal(nodes)
	if err != nil {
		return SnapshotInventory{}, err
	}
	return SnapshotInventory{Entries: len(nodes), LogicalBytes: total, Digest: sum(b)}, nil
}

func openSnapshotArtifact(parent *os.File, name string) (*os.File, SubvolumeIdentity, error) {
	if filepath.Base(name) != name || name == "." || name == ".." || len(name) > 255 {
		return nil, SubvolumeIdentity{}, fmt.Errorf("invalid snapshot artifact name")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, SubvolumeIdentity{}, err
	}
	d := os.NewFile(uintptr(fd), name)
	id, err := nativeSubvolume(d)
	if err != nil {
		d.Close()
		return nil, id, err
	}
	return d, id, nil
}

func (e *Engine) checkSnapshotCapacity(ctx context.Context, op *Operation, dir *os.File) error {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &fs); err != nil {
		return err
	}
	available, err := checkedProduct(uint64(fs.Bavail), uint64(fs.Bsize))
	if err != nil {
		return err
	}
	p := op.Plan.Snapshot
	metadata, err := checkedProduct(uint64(max(p.Before.Entries, p.Desired.Entries)), 16<<10)
	if err != nil {
		return err
	}
	// A conservative logical-copy allowance plus metadata floor; snapshots
	// share extents, but later CoW writes and metadata exhaustion still need
	// space. This is never described as a reservation or an external backup.
	required := max(p.Before.LogicalBytes, p.Desired.LogicalBytes) + metadata + (8 << 20)
	reserve := max(e.limits.MinFreeBytes, op.Plan.Limits.MinFreeBytes)
	dev, _, err := dirIdentity(dir)
	if err != nil {
		return err
	}
	passed := available >= reserve && required <= available-reserve
	evidence := CapacityEvidence{Passed: passed, Filesystems: []FilesystemCapacity{{Path: p.Config.Store, Device: dev, AvailableBytes: available, RequiredBytes: required, ReserveBytes: reserve, BlockBytes: int64(fs.Bsize)}}}
	b, _ := json.Marshal(evidence)
	check, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "snapshot_capacity", b)
	if err != nil {
		return err
	}
	op.Checks = append(op.Checks, check)
	if !passed {
		return fmt.Errorf("insufficient Btrfs capacity for snapshot metadata, copy-on-write headroom and operator reserve")
	}
	return nil
}
