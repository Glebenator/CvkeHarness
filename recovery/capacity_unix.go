//go:build linux || darwin

package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"golang.org/x/sys/unix"
)

type FilesystemCapacity struct {
	Path           string `json:"path"`
	Device         uint64 `json:"device"`
	AvailableBytes int64  `json:"available_bytes"`
	RequiredBytes  int64  `json:"required_bytes"`
	ReserveBytes   int64  `json:"reserve_bytes"`
	BlockBytes     int64  `json:"block_bytes"`
}

type CapacityEvidence struct {
	Passed      bool                 `json:"passed"`
	Filesystems []FilesystemCapacity `json:"filesystems"`
}

// checkCapacity samples the actual target filesystems immediately before
// application. Candidate and restoration staging are both budgeted, without
// crediting deletion/quarantine as free space. This is a conservative check,
// not a reservation against other writers or a quota/disk-failure guarantee.
func (e *Engine) checkCapacity(ctx context.Context, op *Operation) error {
	grouped := map[uint64]*FilesystemCapacity{}
	for _, en := range op.Plan.Entries {
		d, err := e.entryParent(en)
		if err != nil {
			return err
		}
		var fs unix.Statfs_t
		err = unix.Fstatfs(int(d.Fd()), &fs)
		d.Close()
		if err != nil {
			return fmt.Errorf("measure target capacity: %w", err)
		}
		block := int64(fs.Bsize)
		if block < 512 || block > 1<<20 {
			return fmt.Errorf("unsupported target allocation block size")
		}
		available, err := checkedProduct(uint64(fs.Bavail), uint64(block))
		if err != nil {
			return err
		}
		cost, err := stagingCapacity(en.Before.Bytes, en.After.Bytes, block)
		if err != nil {
			return err
		}
		capacity, ok := grouped[en.ParentDevice]
		if !ok {
			capacity = &FilesystemCapacity{Path: en.Root, Device: en.ParentDevice, AvailableBytes: available, BlockBytes: block, ReserveBytes: max(e.limits.MinFreeBytes, op.Plan.Limits.MinFreeBytes)}
			grouped[en.ParentDevice] = capacity
		} else {
			if capacity.BlockBytes != block {
				return fmt.Errorf("filesystem block geometry changed")
			}
			capacity.AvailableBytes = min(capacity.AvailableBytes, available)
		}
		if cost > math.MaxInt64-capacity.RequiredBytes {
			return fmt.Errorf("capacity addition overflow")
		}
		capacity.RequiredBytes += cost
	}
	evidence := CapacityEvidence{Passed: true}
	for _, c := range grouped {
		if c.AvailableBytes < c.ReserveBytes || c.RequiredBytes > c.AvailableBytes-c.ReserveBytes {
			evidence.Passed = false
		}
		evidence.Filesystems = append(evidence.Filesystems, *c)
	}
	sort.Slice(evidence.Filesystems, func(i, j int) bool { return evidence.Filesystems[i].Device < evidence.Filesystems[j].Device })
	b, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	check, err := e.store.RecordRecoveryCheck(ctx, op.RecoveryOperation, "file_capacity", b)
	if err != nil {
		return err
	}
	op.Checks = append(op.Checks, check)
	if !evidence.Passed {
		return fmt.Errorf("insufficient fresh target capacity for candidate staging, restoration headroom and operator reserve; target unchanged")
	}
	return e.checkpoint("capacity_checked", -1)
}

func stagingCapacity(before, after, block int64) (int64, error) {
	if before < 0 || after < 0 || block < 512 || block > 1<<20 {
		return 0, fmt.Errorf("invalid capacity operands")
	}
	// Ceil each file independently to allocation blocks, then leave eight
	// extra blocks per entry for directory/staging metadata. No truncation.
	var total int64
	for _, size := range []int64{before, after} {
		blocks := size / block
		if size%block != 0 {
			blocks++
		}
		allocated, err := checkedProduct(uint64(blocks), uint64(block))
		if err != nil || allocated > math.MaxInt64-total {
			return 0, errors.Join(fmt.Errorf("staging capacity overflow"), err)
		}
		total += allocated
	}
	if total > math.MaxInt64-8*block {
		return 0, fmt.Errorf("staging overhead overflow")
	}
	return total + 8*block, nil
}
