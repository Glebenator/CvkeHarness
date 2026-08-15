package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		"find . -exec sh -c 'rm -rf x' \\;",
		"env rm -rf ./build",
		"env -u TOKEN rm -rf ./build",
		"sudo -u root rm -rf ./build",
		"sh -c 'rm -rf ./build'",
		"python3 -c 'import os; os.remove(\"x\")'",
		"git clean -fdx",
		"rsync -a --delete source/ dest/",
		"docker system prune -f",
		"kubectl delete namespace test",
		"psql -c 'TRUNCATE users'",
		"curl -XDELETE https://example.test/resource",
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

func TestShellPolicyWildcardDeleteTripsCriticalGuard(t *testing.T) {
	t.Parallel()
	assessment, err := AssessShellCommand("rm -rf ./*", resolvedProfile(t, securitypolicy.ProfileMinimal))
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Decision != securitypolicy.DecisionAsk {
		t.Fatalf("wildcard deletion should require approval under critical-path protection: %#v", assessment)
	}
}

func TestShellPolicyMutatingFlagsAreNotClassifiedAsReads(t *testing.T) {
	t.Parallel()
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	for _, command := range []string{
		"sort -o ./out.txt ./in.txt",
		"sed --in-place=.bak 's/a/b/' ./file.txt",
		"git branch -D obsolete",
		"git remote remove origin",
		"git -C ./repo branch -D obsolete",
	} {
		assessment, err := AssessShellCommand(command, policy)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if assessment.Decision == securitypolicy.DecisionAllow {
			t.Fatalf("%q was misclassified as safe: %#v", command, assessment)
		}
	}
}

func TestShellPolicyProtectsDescendantCreateAppendAndRawDeviceTargets(t *testing.T) {
	t.Parallel()
	minimal := resolvedProfile(t, securitypolicy.ProfileMinimal)
	for _, command := range []string{
		"rm -f /etc/passwd",
		"printf x > /etc/cvkeharness-new.conf",
		"printf x >> /etc/sudoers",
		"tee /dev/disk999",
		"cp ./image /dev/sda",
	} {
		assessment, err := AssessShellCommand(command, minimal)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if assessment.Decision == securitypolicy.DecisionAllow {
			t.Fatalf("%q bypassed protected target controls: %#v", command, assessment)
		}
	}
	lessStrict := resolvedProfile(t, securitypolicy.ProfileLessStrict)
	assessment, err := AssessShellCommand("tee /dev/disk999", lessStrict)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Decision != securitypolicy.DecisionDeny || !hasEffect(assessment.Effects, securitypolicy.SettingRawDeviceAccess) {
		t.Fatalf("raw device write must follow the deny control: %#v", assessment)
	}
}

func TestShellPolicyDynamicWritesAndUntrustedExecutablesRequireReview(t *testing.T) {
	dir := t.TempDir()
	fakeLS := filepath.Join(dir, "ls")
	if err := os.WriteFile(fakeLS, []byte("#!/bin/sh\nrm -rf ./victim\n"), 0700); err != nil {
		t.Fatal(err)
	}
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	for _, command := range []string{
		"printf x > ./existing*",
		"printf x > $TARGET",
		fakeLS + " -la",
		"./sort ./input",
		"python3 cleanup.py",
	} {
		assessment, err := AssessShellCommand(command, policy)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if assessment.Decision == securitypolicy.DecisionAllow || !hasEffect(assessment.Effects, securitypolicy.SettingUnknownCommands) {
			t.Fatalf("%q must require review as unresolved/opaque: %#v", command, assessment)
		}
	}
}

func TestShellPolicyQuotedAndOptionTargetsCannotHideProtectedWrites(t *testing.T) {
	t.Parallel()
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	for _, command := range []string{
		`printf x > "/etc/file with spaces"`,
		"cp --target-directory=/etc ./source",
		"mv -t /etc ./source",
		"install -t /etc ./source",
		"ln --target-directory=/etc ./source",
		"find . -fprint /etc/cvke-find-output",
		"find . -fprintf /etc/cvke-find-output '%p'",
		"tee /etc/passwd /tmp/cvke-output",
		"touch /etc/cvke-a /tmp/cvke-b",
		"mkdir /etc/cvke-dir /tmp/cvke-dir",
		"truncate /etc/passwd /tmp/cvke-output",
	} {
		assessment, err := AssessShellCommand(command, policy)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if assessment.Decision == securitypolicy.DecisionAllow {
			t.Fatalf("%q hid a protected write target: %#v", command, assessment)
		}
	}
}

func TestShellPolicyRejectsProcessSubstitutionAndEscalatesBraceTargets(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		"bash -c 'cat <(rm -rf /System/some-target)'",
		"bash -c 'cat >(rm -rf /System/some-target)'",
	} {
		if _, err := AssessShellCommand(command, resolvedProfile(t, securitypolicy.ProfileLessStrict)); err == nil {
			t.Fatalf("process substitution must be rejected: %q", command)
		}
	}
	assessment, err := AssessShellCommand("rm -rf /{tmp,System}", resolvedProfile(t, securitypolicy.ProfileMinimal))
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Decision != securitypolicy.DecisionAsk {
		t.Fatalf("brace-expanded delete must trip the critical guard: %#v", assessment)
	}
}

func TestLLMReviewIsAdvisoryAndStillRequiresHumanApproval(t *testing.T) {
	t.Parallel()
	policy := resolvedProfile(t, securitypolicy.ProfileLessStrict)
	tool := NewShellToolWithApprover(nil, nil, "")
	tool.applySecurityPolicy(policy, NewBlockingApprover(), staticApprover{decision: ShellApprovalDecision{Approved: true, Mode: SafetyModeLLMJudge}})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"opaque-tool --flag"}`))
	if _, ok := IsApprovalRequired(err); !ok {
		t.Fatalf("LLM review must remain advisory and require a person, got %v", err)
	}
}

func TestStreamCaptureRedactsBeforeVisibleTruncation(t *testing.T) {
	t.Parallel()
	stream := newStreamCaptureWriter(context.Background(), 12)
	_, _ = stream.Write([]byte("xx sk-abcdefghijklmnopqrstuvwxyz"))
	result := stream.Result()
	if strings.Contains(result, "sk-") {
		t.Fatalf("secret leaked through truncation boundary: %q", result)
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
