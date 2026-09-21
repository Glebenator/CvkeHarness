// Package recovery executes explicitly prepared, bounded system changes and
// restores them without relying on model-generated compensation commands.
package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

const (
	Preparing            = "preparing"
	Ready                = "ready"
	Applying             = "applying"
	Verifying            = "verifying"
	Committed            = "committed"
	Unknown              = "outcome_unknown"
	RollingBack          = "rolling_back"
	Recovered            = "recovered"
	RecoveryFailed       = "recovery_failed"
	PreparationFailed    = "preparation_failed"
	Validating           = "validating"
	ValidationFailed     = "validation_failed"
	SSHArmed             = "ssh_armed"
	AwaitingConfirmation = "awaiting_confirmation"
)

type Limits struct {
	MaxRepairAttempts int   `json:"max_repair_attempts,omitzero" yaml:"max_repair_attempts"`
	MaxFiles          int   `json:"max_files" yaml:"max_files"`
	MaxBytes          int64 `json:"max_bytes" yaml:"max_bytes"`
	MaxFileBytes      int64 `json:"max_file_bytes" yaml:"max_file_bytes"`
	MaxAgeSeconds     int64 `json:"max_age_seconds" yaml:"max_age_seconds"`
	MinFreeBytes      int64 `json:"min_free_bytes,omitempty" yaml:"min_free_bytes"`
}

func DefaultLimits() Limits {
	return Limits{MaxFiles: 100, MaxBytes: 32 << 20, MaxFileBytes: 8 << 20, MaxAgeSeconds: 900, MinFreeBytes: 16 << 20, MaxRepairAttempts: 2}
}

func (l Limits) validate() error {
	if l.MaxRepairAttempts < 0 || l.MaxRepairAttempts > 3 {
		return fmt.Errorf("model repair attempt limit must be 1..3 (zero uses 2)")
	}
	if l.MaxFiles < 1 || l.MaxFiles > 10000 || l.MaxBytes < 1 || l.MaxBytes > 1<<30 || l.MaxFileBytes < 1 || l.MaxFileBytes > l.MaxBytes || l.MaxAgeSeconds < 1 || l.MaxAgeSeconds > 86400 || l.MinFreeBytes < 0 || l.MinFreeBytes > 1<<40 {
		return fmt.Errorf("invalid recovery limits (maximum 10000 files, 1 GiB, 24 hours)")
	}
	return nil
}

type Change struct {
	Action         string `json:"action"` // replace, create, delete
	Path           string `json:"path"`
	Content        string `json:"content,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	Mode           uint32 `json:"mode,omitempty"` // create only; octal permissions encoded as JSON integer
}

type Request struct {
	Changes    []Change `json:"changes"`
	Service    string   `json:"service,omitempty"` // operator-defined NGINX instance
	SSHService string   `json:"ssh_service,omitempty"`
}

// Fingerprint includes content and the metadata this backend can restore.
// Unsupported xattrs, ACLs, special mode bits, hardlinks and nonregular files
// are rejected rather than silently stripped.
type Fingerprint struct {
	Exists     bool   `json:"exists"`
	SHA256     string `json:"sha256,omitempty"`
	Bytes      int64  `json:"bytes"`
	Mode       uint32 `json:"mode"`
	UID        uint32 `json:"uid"`
	GID        uint32 `json:"gid"`
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
	ModifiedNS int64  `json:"modified_ns"`
}

type Entry struct {
	Action       string      `json:"action"`
	Path         string      `json:"path"`
	Root         string      `json:"root"`
	ParentDevice uint64      `json:"parent_device"`
	ParentInode  uint64      `json:"parent_inode"`
	Before       Fingerprint `json:"before"`
	After        Fingerprint `json:"after"`
	Backup       string      `json:"backup"`
	Candidate    string      `json:"candidate"`
	Quarantine   string      `json:"quarantine,omitempty"`
}

type Manifest struct {
	Version   int           `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	Limits    Limits        `json:"limits"`
	Entries   []Entry       `json:"entries"`
	Service   *ServicePlan  `json:"service,omitempty"`
	Snapshot  *SnapshotPlan `json:"snapshot,omitempty"`
	SSH       *SSHPlan      `json:"ssh,omitempty"`
}

type Operation struct {
	state.RecoveryOperation
	Plan   Manifest              `json:"plan"`
	Checks []state.RecoveryCheck `json:"checks,omitempty"`
}

func sum(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func planDigest(target, policy string, m Manifest) string {
	b, _ := json.Marshal(struct {
		Target, Policy string
		Plan           Manifest
	}{target, policy, m})
	return sum(b)
}

func encodePlan(op *Operation) error {
	b, err := json.Marshal(op.Plan)
	op.Manifest = b
	return err
}
