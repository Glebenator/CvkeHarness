// Package securitypolicy resolves named security bundles and per-setting
// overrides into one immutable runtime policy.
package securitypolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const SchemaVersion = 1

type Profile string

const (
	ProfileExtraStrict Profile = "extra_strict"
	ProfileReasonable  Profile = "reasonable"
	ProfileLessStrict  Profile = "less_strict"
	ProfileMinimal     Profile = "minimal"
	ProfileYOLO        Profile = "yolo"
)

type Decision string

const (
	DecisionAllow     Decision = "allow"
	DecisionLLMReview Decision = "llm_review"
	DecisionAsk       Decision = "ask"
	DecisionDeny      Decision = "deny"
)

const (
	SettingReadCommands        = "commands.read"
	SettingUnknownCommands     = "commands.unknown"
	SettingScriptExecution     = "commands.scripts"
	SettingFileCreate          = "filesystem.create"
	SettingFileOverwrite       = "filesystem.overwrite"
	SettingFileAppend          = "filesystem.append"
	SettingFileDelete          = "filesystem.delete"
	SettingPrivilegeEscalation = "system.privilege_escalation"
	SettingServiceChanges      = "system.service_changes"
	SettingPackageChanges      = "system.package_changes"
	SettingRawDeviceAccess     = "system.raw_devices"
	SettingNetworkAccess       = "network.outbound"
	SettingRemoteMutation      = "remote.mutations"
	SettingCloudChanges        = "remote.cloud_changes"
	SettingContainerChanges    = "remote.container_changes"
	SettingDatabaseDestructive = "remote.database_destructive"
	SettingScheduledChanges    = "autonomy.scheduled_changes"
	SettingCredentialAccess    = "data.credential_access"
	SettingRememberApprovals   = "approval.reuse_this_session"
	SettingProtectCritical     = "filesystem.protect_critical_paths"
	SettingProtectCredentials  = "filesystem.protect_credential_paths"
	SettingMaxCommandBytes     = "limits.command_bytes"
	SettingMaxSegments         = "limits.command_segments"
	SettingTimeoutSeconds      = "limits.timeout_seconds"
	SettingMaxOutputBytes      = "limits.output_bytes"
)

type ValueKind string

const (
	KindDecision ValueKind = "decision"
	KindBool     ValueKind = "bool"
	KindInt      ValueKind = "int"
)

type Setting struct {
	ID          string
	Category    string
	Label       string
	Description string
	Kind        ValueKind
	Min         int
	Max         int
}

// Selection is the durable user choice. Overrides contain only values that
// differ from (or intentionally restate) the selected profile.
type Selection struct {
	Version   int               `yaml:"version,omitempty" json:"version"`
	Profile   Profile           `yaml:"profile" json:"profile"`
	Overrides map[string]string `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}

// EffectivePolicy is a fully resolved snapshot used by one runtime session.
// Values is intentionally unexported through YAML so execution never consults
// a partly resolved persisted structure.
type EffectivePolicy struct {
	Profile Profile
	Values  map[string]string
	Origins map[string]string
	Hash    string
}

type Bundle struct {
	ID          Profile
	Label       string
	Description string
	Values      map[string]string
}

var settings = []Setting{
	{SettingReadCommands, "Commands", "Read-only commands", "Known diagnostics and inspection commands", KindDecision, 0, 0},
	{SettingUnknownCommands, "Commands", "Unknown commands", "Commands whose effects cannot be classified", KindDecision, 0, 0},
	{SettingScriptExecution, "Commands", "Script interpreters", "Shell, Python, Node, Ruby, and Perl script execution", KindDecision, 0, 0},
	{SettingFileCreate, "Filesystem", "Create files", "Create a path that does not already exist", KindDecision, 0, 0},
	{SettingFileOverwrite, "Filesystem", "Overwrite files", "Replace or truncate an existing path", KindDecision, 0, 0},
	{SettingFileAppend, "Filesystem", "Append to files", "Append data to an existing or new path", KindDecision, 0, 0},
	{SettingFileDelete, "Filesystem", "Delete files", "Remove files, directories, or repository content", KindDecision, 0, 0},
	{SettingProtectCritical, "Filesystem", "Protect critical paths", "Escalate writes or deletes targeting roots, system areas, repositories, or backups", KindBool, 0, 0},
	{SettingProtectCredentials, "Filesystem", "Protect credential paths", "Escalate access to known keys, tokens, histories, and credential stores", KindBool, 0, 0},
	{SettingPrivilegeEscalation, "System", "Privilege escalation", "sudo, su, doas, permission, and ownership changes", KindDecision, 0, 0},
	{SettingServiceChanges, "System", "Service changes", "Start, stop, restart, enable, or disable services", KindDecision, 0, 0},
	{SettingPackageChanges, "System", "Package changes", "Install, remove, upgrade, or publish packages", KindDecision, 0, 0},
	{SettingRawDeviceAccess, "System", "Raw device access", "Format, wipe, mount, or write block devices", KindDecision, 0, 0},
	{SettingNetworkAccess, "Network and remote", "Outbound network", "Network probes, downloads, and remote reads", KindDecision, 0, 0},
	{SettingRemoteMutation, "Network and remote", "Remote mutations", "Commands that can change a remote system", KindDecision, 0, 0},
	{SettingCloudChanges, "Network and remote", "Cloud changes", "AWS, Azure, GCP, Terraform, and Kubernetes resource changes", KindDecision, 0, 0},
	{SettingContainerChanges, "Network and remote", "Container changes", "Docker, Podman, and Kubernetes workload mutations", KindDecision, 0, 0},
	{SettingDatabaseDestructive, "Network and remote", "Destructive database", "DROP, TRUNCATE, and unbounded DELETE operations", KindDecision, 0, 0},
	{SettingScheduledChanges, "Autonomy", "Scheduled changes", "Cron, at, timers, and durable background-job mutations", KindDecision, 0, 0},
	{SettingCredentialAccess, "Data", "Credential access", "Read or expose secret-bearing files, environment, or process data", KindDecision, 0, 0},
	{SettingRememberApprovals, "Approvals", "Reuse approvals", "Reuse an exact human-approved command during this process only", KindBool, 0, 0},
	{SettingMaxCommandBytes, "Limits", "Command bytes", "Maximum UTF-8 command size before execution", KindInt, 256, 65536},
	{SettingMaxSegments, "Limits", "Command segments", "Maximum commands in one chained shell action", KindInt, 1, 64},
	{SettingTimeoutSeconds, "Limits", "Command timeout", "Maximum wall-clock seconds for one shell action", KindInt, 1, 3600},
	{SettingMaxOutputBytes, "Limits", "Captured output", "Maximum combined stdout and stderr retained or emitted", KindInt, 512, 1048576},
}

func Catalog() []Setting {
	out := make([]Setting, len(settings))
	copy(out, settings)
	return out
}

func Profiles() []Bundle {
	ids := []Profile{ProfileExtraStrict, ProfileReasonable, ProfileLessStrict, ProfileMinimal, ProfileYOLO}
	out := make([]Bundle, 0, len(ids))
	for _, id := range ids {
		bundle, _ := BundleFor(id)
		out = append(out, bundle)
	}
	return out
}

func BundleFor(profile Profile) (Bundle, bool) {
	values, ok := bundleValues()[profile]
	if !ok {
		return Bundle{}, false
	}
	labels := map[Profile][2]string{
		ProfileExtraStrict: {"Extra strict", "Known reads run; opaque and destructive actions are denied"},
		ProfileReasonable:  {"Reasonable", "Reads run; mutations ask; credentials and raw devices stay blocked"},
		ProfileLessStrict:  {"Less strict", "Routine recoverable changes run; destructive and remote changes still ask"},
		ProfileMinimal:     {"Minimal", "Most actions run; critical paths and credentials still interrupt"},
		ProfileYOLO:        {"YOLO", "No CvkeHarness approval gates; operating-system protections still apply"},
	}
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	return Bundle{ID: profile, Label: labels[profile][0], Description: labels[profile][1], Values: copyValues}, true
}

func DefaultSelection() *Selection {
	return &Selection{Version: SchemaVersion, Profile: ProfileReasonable}
}

func (s *Selection) Clone() *Selection {
	if s == nil {
		return nil
	}
	out := *s
	if s.Overrides != nil {
		out.Overrides = make(map[string]string, len(s.Overrides))
		for key, value := range s.Overrides {
			out.Overrides[key] = value
		}
	}
	return &out
}

func (s *Selection) Normalize() {
	if s.Version == 0 {
		s.Version = SchemaVersion
	}
	if s.Profile == "" {
		s.Profile = ProfileReasonable
	}
	for key, value := range s.Overrides {
		s.Overrides[key] = strings.ToLower(strings.TrimSpace(value))
	}
}

func (s *Selection) Validate() error {
	if s == nil {
		return fmt.Errorf("security selection is required")
	}
	if s.Version != SchemaVersion {
		return fmt.Errorf("unsupported security schema version %d", s.Version)
	}
	if _, ok := BundleFor(s.Profile); !ok {
		return fmt.Errorf("unknown security profile %q", s.Profile)
	}
	for id, value := range s.Overrides {
		setting, ok := SettingByID(id)
		if !ok {
			return fmt.Errorf("unknown security setting %q", id)
		}
		if err := validateValue(setting, value); err != nil {
			return fmt.Errorf("security setting %s: %w", id, err)
		}
	}
	return nil
}

func Resolve(selection *Selection) (EffectivePolicy, error) {
	if selection == nil {
		selection = DefaultSelection()
	}
	selection = selection.Clone()
	selection.Normalize()
	if err := selection.Validate(); err != nil {
		return EffectivePolicy{}, err
	}
	bundle, _ := BundleFor(selection.Profile)
	effective := EffectivePolicy{
		Profile: selection.Profile,
		Values:  make(map[string]string, len(bundle.Values)),
		Origins: make(map[string]string, len(bundle.Values)),
	}
	for key, value := range bundle.Values {
		effective.Values[key] = value
		effective.Origins[key] = "profile"
	}
	for key, value := range selection.Overrides {
		effective.Values[key] = value
		effective.Origins[key] = "override"
	}
	payload, _ := json.Marshal(struct {
		Version int               `json:"version"`
		Profile Profile           `json:"profile"`
		Values  map[string]string `json:"values"`
	}{SchemaVersion, effective.Profile, effective.Values})
	sum := sha256.Sum256(payload)
	effective.Hash = hex.EncodeToString(sum[:8])
	return effective, nil
}

func (p EffectivePolicy) Value(id string) string { return p.Values[id] }

func (p EffectivePolicy) Decision(id string) Decision {
	return Decision(p.Values[id])
}

func (p EffectivePolicy) Bool(id string) bool {
	value, _ := strconv.ParseBool(p.Values[id])
	return value
}

func (p EffectivePolicy) Int(id string) int {
	value, _ := strconv.Atoi(p.Values[id])
	return value
}

func (p EffectivePolicy) OverrideCount() int {
	count := 0
	for _, origin := range p.Origins {
		if origin == "override" {
			count++
		}
	}
	return count
}

func (p EffectivePolicy) Summary() string {
	label := strings.ReplaceAll(string(p.Profile), "_", " ")
	if count := p.OverrideCount(); count > 0 {
		return fmt.Sprintf("%s + %d override%s", label, count, plural(count))
	}
	return label
}

func (s *Selection) SetOverride(id, value string) error {
	setting, ok := SettingByID(id)
	if !ok {
		return fmt.Errorf("unknown security setting %q", id)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if err := validateValue(setting, value); err != nil {
		return err
	}
	if s.Overrides == nil {
		s.Overrides = make(map[string]string)
	}
	s.Overrides[id] = value
	return nil
}

func (s *Selection) ClearOverride(id string) {
	delete(s.Overrides, id)
}

func (s *Selection) ApplyProfile(profile Profile) error {
	if _, ok := BundleFor(profile); !ok {
		return fmt.Errorf("unknown security profile %q", profile)
	}
	s.Version = SchemaVersion
	s.Profile = profile
	s.Overrides = nil
	return nil
}

func SettingByID(id string) (Setting, bool) {
	for _, setting := range settings {
		if setting.ID == id {
			return setting, true
		}
	}
	return Setting{}, false
}

func NextValue(setting Setting, current string, delta int) string {
	var values []string
	switch setting.Kind {
	case KindDecision:
		values = []string{string(DecisionAllow), string(DecisionLLMReview), string(DecisionAsk), string(DecisionDeny)}
	case KindBool:
		values = []string{"false", "true"}
	case KindInt:
		values = intSettingOptions(setting.ID)
	}
	for index, value := range values {
		if value == current {
			return values[(index+delta+len(values))%len(values)]
		}
	}
	return values[0]
}

func intSettingOptions(id string) []string {
	switch id {
	case SettingMaxCommandBytes:
		return []string{"1024", "4096", "8192", "16384", "32768", "65536"}
	case SettingMaxSegments:
		return []string{"1", "4", "8", "16", "24", "32", "64"}
	case SettingTimeoutSeconds:
		return []string{"5", "15", "30", "60", "120", "300", "900", "3600"}
	case SettingMaxOutputBytes:
		return []string{"512", "4096", "8192", "16384", "32768", "65536", "262144", "1048576"}
	default:
		return nil
	}
}

func OverrideKeys(selection *Selection) []string {
	if selection == nil {
		return nil
	}
	keys := make([]string, 0, len(selection.Overrides))
	for key := range selection.Overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validateValue(setting Setting, value string) error {
	switch setting.Kind {
	case KindDecision:
		switch Decision(value) {
		case DecisionAllow, DecisionLLMReview, DecisionAsk, DecisionDeny:
			return nil
		default:
			return fmt.Errorf("must be allow, llm_review, ask, or deny")
		}
	case KindBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("must be true or false")
		}
		return nil
	case KindInt:
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < setting.Min || parsed > setting.Max {
			return fmt.Errorf("must be an integer from %d to %d", setting.Min, setting.Max)
		}
		return nil
	default:
		return fmt.Errorf("has unsupported value kind %q", setting.Kind)
	}
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
