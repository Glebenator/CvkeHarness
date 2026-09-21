//go:build linux

package recovery

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func measureServiceResources(p ServicePlan) (int, int, uint64, error) {
	if err := verifyServiceIdentity(p); err != nil {
		return 0, 0, 0, err
	}
	var affinity unix.CPUSet
	if err := unix.SchedGetaffinity(p.Master.PID, &affinity); err != nil {
		return 0, 0, 0, fmt.Errorf("measure master CPU affinity: %w", err)
	}
	var limit unix.Rlimit
	// nil new-limit is a read. Never raise a target process limit to make an
	// unsafe proposal pass. Workers inherit this soft limit in the supported
	// grammar, which excludes worker_rlimit_nofile and privilege changes.
	if err := unix.Prlimit(p.Master.PID, unix.RLIMIT_NOFILE, nil, &limit); err != nil {
		return 0, 0, 0, fmt.Errorf("measure master descriptor limit: %w", err)
	}
	workers, err := serviceWorkers(p.Master)
	if err != nil {
		return 0, 0, 0, err
	}
	if err = verifyServiceIdentity(p); err != nil {
		return 0, 0, 0, err
	}
	return affinity.Count(), len(workers), limit.Cur, nil
}
