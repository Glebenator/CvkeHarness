package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/securitypolicy"
)

func resolvedProfile(t *testing.T, profile securitypolicy.Profile) securitypolicy.EffectivePolicy {
	t.Helper()
	policy, err := securitypolicy.Resolve(&securitypolicy.Selection{Version: securitypolicy.SchemaVersion, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestShellPolicyProfilesResolveSameDeleteEffectDifferently(t *testing.T) {
	t.Parallel()
	want := map[securitypolicy.Profile]securitypolicy.Decision{
		securitypolicy.ProfileExtraStrict: securitypolicy.DecisionDeny,
		securitypolicy.ProfileReasonable:  securitypolicy.DecisionAsk,
		securitypolicy.ProfileLessStrict:  securitypolicy.DecisionAsk,
		securitypolicy.ProfileMinimal:     securitypolicy.DecisionAllow,
		securitypolicy.ProfileYOLO:        securitypolicy.DecisionAllow,
	}
	for profile, expected := range want {
		assessment, err := AssessShellCommand("rm -f ./ordinary-test-file", resolvedProfile(t, profile))
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		if assessment.Decision != expected {
			t.Fatalf("%s delete decision = %q, want %q (%s)", profile, assessment.Decision, expected, assessment.Reason)
		}
	}
}

func TestShellPolicyClassifiesRedirectionByEffect(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existing, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	cases := []struct {
		command string
		setting string
		want    securitypolicy.Decision
	}{
		{"printf ok 2>/dev/null", securitypolicy.SettingReadCommands, securitypolicy.DecisionAllow},
		{"printf ok 2>&1", securitypolicy.SettingReadCommands, securitypolicy.DecisionAllow},
		{"printf ok > " + filepath.Join(dir, "new.txt"), securitypolicy.SettingFileCreate, securitypolicy.DecisionAllow},
		{"printf ok > " + existing, securitypolicy.SettingFileOverwrite, securitypolicy.DecisionAsk},
		{"printf ok >> " + existing, securitypolicy.SettingFileAppend, securitypolicy.DecisionAllow},
	}
	for _, tc := range cases {
		assessment, err := AssessShellCommand(tc.command, policy)
		if err != nil {
			t.Fatalf("%q: %v", tc.command, err)
		}
		if assessment.Decision != tc.want {
			t.Fatalf("%q decision = %q, want %q; effects=%#v", tc.command, assessment.Decision, tc.want, assessment.Effects)
		}
		if !hasEffect(assessment.Effects, tc.setting) {
			t.Fatalf("%q missing effect %s: %#v", tc.command, tc.setting, assessment.Effects)
		}
	}
}

func TestShellPolicyFixesBareSystemctlAndJournalctlAllowlistHole(t *testing.T) {
	t.Parallel()
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	cases := []struct {
		command string
		effect  string
	}{
		{"systemctl restart sshd", securitypolicy.SettingServiceChanges},
		{"journalctl --vacuum-time=1d", securitypolicy.SettingFileDelete},
	}
	for _, tc := range cases {
		assessment, err := AssessShellCommand(tc.command, policy)
		if err != nil {
			t.Fatal(err)
		}
		if assessment.Decision == securitypolicy.DecisionAllow || !hasEffect(assessment.Effects, tc.effect) {
			t.Fatalf("%q was not gated: %#v", tc.command, assessment)
		}
	}
	for _, command := range []string{"systemctl status sshd", "journalctl -n 25"} {
		assessment, err := AssessShellCommand(command, policy)
		if err != nil || assessment.Decision != securitypolicy.DecisionAllow {
			t.Fatalf("%q should be a known read: %#v err=%v", command, assessment, err)
		}
	}
}

func TestShellPolicyDiagnosticCommandIsReviewedByEffectsNotGreaterThanGlyph(t *testing.T) {
	t.Parallel()
	command := "set -e\nprintf '%s\\n' '== Disk =='\ndf -h / /System/Volumes/Data\nsudo -n du -xhd 1 /System/Volumes/Data 2>/dev/null | sort -h || du -xhd 1 /System/Volumes/Data 2>/dev/null | sort -h\ntmutil listlocalsnapshots / 2>&1 || true"
	assessment, err := AssessShellCommand(command, resolvedProfile(t, securitypolicy.ProfileReasonable))
	if err != nil {
		t.Fatalf("diagnostic command should parse: %v", err)
	}
	if assessment.Decision == securitypolicy.DecisionDeny {
		t.Fatalf("diagnostic command was blindly denied: %#v", assessment)
	}
	if !hasEffect(assessment.Effects, securitypolicy.SettingPrivilegeEscalation) {
		t.Fatalf("expected sudo to require approval by effect: %#v", assessment.Effects)
	}
}

func TestShellPolicyDetectsDeletionBypasses(t *testing.T) {
	t.Parallel()
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	commands := []string{
		"find . -delete",
		"env rm -rf ./build",
		"sh -c 'rm -rf ./build'",
		"python3 -c 'import os; os.remove(\"x\")'",
		"git clean -fdx",
		"rsync -a --delete source/ dest/",
		"docker system prune -f",
		"kubectl delete namespace test",
		"psql -c 'TRUNCATE users'",
	}
	for _, command := range commands {
		assessment, err := AssessShellCommand(command, policy)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if assessment.Decision == securitypolicy.DecisionAllow {
			t.Fatalf("%q unexpectedly allowed: %#v", command, assessment)
		}
	}
}

func TestShellPolicyCriticalPathGuardStillInterruptsMinimal(t *testing.T) {
	t.Parallel()
	assessment, err := AssessShellCommand("rm -rf /", resolvedProfile(t, securitypolicy.ProfileMinimal))
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Decision != securitypolicy.DecisionAsk {
		t.Fatalf("minimal root delete = %q, want ask: %s", assessment.Decision, assessment.Reason)
	}
	yolo, err := AssessShellCommand("rm -rf /", resolvedProfile(t, securitypolicy.ProfileYOLO))
	if err != nil {
		t.Fatal(err)
	}
	if yolo.Decision != securitypolicy.DecisionAllow {
		t.Fatalf("YOLO must honestly disable CvkeHarness gate, got %q", yolo.Decision)
	}
}

func hasEffect(effects []ShellEffect, setting string) bool {
	for _, effect := range effects {
		if effect.Setting == setting {
			return true
		}
	}
	return false
}
