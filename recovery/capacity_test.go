//go:build linux || darwin

package recovery

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
)

func TestCapacityRoundsUpAndChecksOverflow(t *testing.T) {
	got, err := stagingCapacity(1, 4097, 4096)
	if err != nil || got != 11*4096 {
		t.Fatalf("capacity = %d / %v", got, err)
	}
	if _, err := stagingCapacity(math.MaxInt64, 0, 4096); err == nil {
		t.Fatal("round-up overflow accepted")
	}
	if _, err := stagingCapacity(-1, 1, 4096); err == nil {
		t.Fatal("negative resource size accepted")
	}
}

func TestFreshCapacityRefusalPersistsEvidenceBeforeMutation(t *testing.T) {
	e, root := fixture(t)
	path := filepath.Join(root, "settings")
	put(t, path, "original")
	op := prepare(t, e, Change{Action: "replace", Path: path, Content: "changed"})
	// Inject an impossible reserve after preparation. This isolates the fresh
	// apply-time check from unrelated host filesystem size or free space.
	e.limits.MinFreeBytes = math.MaxInt64
	op, err := e.Apply(context.Background(), op.ID, op.Digest)
	if err == nil || op.Status != Ready {
		t.Fatalf("capacity refusal: %s / %v", op.Status, err)
	}
	content(t, path, "original")
	inspected, err := e.Inspect(context.Background(), op.ID)
	if err != nil || len(inspected.Checks) != 1 {
		t.Fatalf("capacity evidence missing: %#v / %v", inspected.Checks, err)
	}
	var evidence CapacityEvidence
	if err := json.Unmarshal(inspected.Checks[0].Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Passed || len(evidence.Filesystems) != 1 || evidence.Filesystems[0].AvailableBytes < 0 || evidence.Filesystems[0].RequiredBytes <= 0 {
		t.Fatalf("invalid fresh evidence: %#v", evidence)
	}
	// A new measurement is made on the next explicitly requested application.
	e.limits.MinFreeBytes = DefaultLimits().MinFreeBytes
	applied, err := e.Apply(context.Background(), op.ID, op.Digest)
	if err != nil || applied.Status != Committed || len(applied.Checks) != 2 {
		t.Fatalf("new capacity measurement: %#v / %v", applied.Checks, err)
	}
}
