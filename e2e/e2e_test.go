//go:build e2e && !windows

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
	"github.com/creack/pty"
)

var (
	repositoryRoot string
	testBinaryPath string
	testWorkDir    string
	ansiPattern    = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
)

func TestMain(m *testing.M) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "e2e: resolve test file path")
		os.Exit(2)
	}
	repositoryRoot = filepath.Dir(filepath.Dir(currentFile))

	var err error
	testWorkDir, err = os.MkdirTemp("", "cvkeharness-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create work directory: %v\n", err)
		os.Exit(2)
	}
	testBinaryPath = filepath.Join(testWorkDir, "cvkeharness")

	build := exec.Command("go", "build", "-o", testBinaryPath, ".")
	build.Dir = repositoryRoot
	build.Env = envWith(os.Environ(), map[string]string{
		"GOCACHE": filepath.Join(testWorkDir, "go-build-cache"),
	})
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "e2e: build binary: %v\n%s", buildErr, output)
		_ = os.RemoveAll(testWorkDir)
		os.Exit(2)
	}

	code := m.Run()
	_ = os.RemoveAll(testWorkDir)
	os.Exit(code)
}

func TestCLIHelpShowsPrimaryUserJourneys(t *testing.T) {
	home := t.TempDir()
	output, err := runCLI(t, home, "", "--help")
	if err != nil {
		t.Fatalf("--help failed: %v\n%s", err, output)
	}

	for _, expected := range []string{
		"Run one bounded operations task and exit",
		"setup",
		"run",
		"console",
		"commands",
	} {
		assertContains(t, output, expected)
	}
}

func TestUnconfiguredRunDirectsUserToSetup(t *testing.T) {
	home := t.TempDir()
	output, err := runCLI(t, home, "", "run", "check service status")
	if err == nil {
		t.Fatalf("unconfigured run unexpectedly succeeded:\n%s", output)
	}
	assertContains(t, output, "config file not found")
	assertContains(t, output, "Please run 'cvkeharness setup'")
	if _, statErr := os.Stat(filepath.Join(home, ".cvkeharness", "config.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("failed run should not create a config, stat error=%v", statErr)
	}
}

func TestSetupKeyboardJourneyAtRepresentativeWidths(t *testing.T) {
	for _, width := range []uint16{80, 100, 120} {
		t.Run(fmt.Sprintf("%d_columns", width), func(t *testing.T) {
			home := t.TempDir()
			output := runSetupToProviderAndQuit(t, home, width)
			for _, expected := range []string{
				"Continue to Connect",
				"codex",
				"openrouter",
				"lmstudio",
			} {
				assertContains(t, output, expected)
			}
			if _, err := os.Stat(filepath.Join(home, ".cvkeharness", "config.yaml")); !os.IsNotExist(err) {
				t.Fatalf("quitting setup before review should not save a config, stat error=%v", err)
			}
		})
	}
}

func TestChatLocalCommandsStayLocal(t *testing.T) {
	home := t.TempDir()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "local commands must not reach the model", http.StatusInternalServerError)
	}))
	defer server.Close()
	paths := writeConfig(t, home, server.URL+"/v1")

	output := runConsoleChatJourney(t, home, []consoleChatStep{
		{input: "/help", waitFor: "Start a fresh in-process chat session"},
		{input: "/memory", waitFor: "No memory has been retrieved yet"},
		{input: "/tools", waitFor: "Availability is not authorization"},
		{input: "/missing", waitFor: "Unknown command: /missing"},
	})

	for _, expected := range []string{
		"Chat",
		"/memory",
		"No memory has been retrieved yet",
		"Registered tools",
		"Availability is not authorization",
		"shell_execute",
		"Unknown command",
		"Prefix with //",
	} {
		assertContains(t, output, expected)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("local slash commands made %d model requests, want zero", got)
	}

	store := state.Open(paths.stateDB)
	defer store.Close()
	if !store.Available() {
		t.Fatalf("state database unavailable: %v", store.Err())
	}
	sessions, err := store.ListRecentChatSessions(context.Background(), 5)
	if err != nil {
		t.Fatalf("list chat sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].TurnCount != 0 || sessions[0].ExitReason != "tui_exit" {
		t.Fatalf("unexpected local-command session: %#v", sessions)
	}
}

func TestToolBackedChatPersistsAndExportsVerifiedTurn(t *testing.T) {
	home := t.TempDir()
	model := newMockModelServer(t)
	defer model.Close()
	paths := writeConfig(t, home, model.URL+"/v1")

	output := runConsoleChatJourney(t, home, []consoleChatStep{
		{input: "run the shell command echo E2E_TOOL_OK then confirm the result", waitFor: "Tool-backed response complete."},
		{input: "/export", waitFor: "Export complete:"},
	})

	for _, expected := range []string{
		"echo E2E_TOOL_OK",
		"E2E_TOOL_OK",
		"Tool-backed response complete.",
		"Export complete",
		"Private file (0600)",
		"SUCCEEDED",
	} {
		assertContains(t, output, expected)
	}

	requests := model.Requests()
	if len(requests) != 4 {
		t.Fatalf("model received %d requests, want classifier, tool call, final response, and verifier", len(requests))
	}
	if len(requests[0].Tools) != 0 || !strings.Contains(lastMessage(requests[0]).Content, "current_user_prompt") {
		t.Fatalf("first model request was not the advisory task classifier: %#v", requests[0])
	}
	if !toolAdvertised(requests[1], "shell_execute") {
		t.Fatalf("second model request did not advertise shell_execute: %#v", requests[1].Tools)
	}
	if last := lastMessage(requests[2]); last.Role != "tool" || !strings.Contains(last.Content, "E2E_TOOL_OK") {
		t.Fatalf("third model request did not contain the shell result: %#v", last)
	}
	if last := lastMessage(requests[3]); last.Role != "user" || !strings.Contains(last.Content, "assistant_final_output") {
		t.Fatalf("fourth model request was not completion verification: %#v", last)
	}

	if _, err := os.Stat(paths.stateDB); err != nil {
		t.Fatalf("persisted state DB missing: %v", err)
	}
	store := state.Open(paths.stateDB)
	defer store.Close()
	if !store.Available() {
		t.Fatalf("state database unavailable: %v", store.Err())
	}
	sessions, err := store.ListRecentChatSessions(context.Background(), 5)
	if err != nil {
		t.Fatalf("list chat sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].TurnCount != 1 || sessions[0].ExitReason != "tui_exit" {
		t.Fatalf("unexpected persisted sessions: %#v", sessions)
	}
	detail, err := store.GetChatSessionDetail(context.Background(), sessions[0].ID)
	if err != nil {
		t.Fatalf("get chat detail: %v", err)
	}
	if len(detail.Turns) != 1 {
		t.Fatalf("persisted %d turns, want one", len(detail.Turns))
	}
	turn := detail.Turns[0]
	if turn.FinalOutput != "Tool-backed response complete." || turn.VerificationStatus != "satisfied" || !turn.Success {
		t.Fatalf("unexpected persisted turn: %#v", turn)
	}
	outcomes := detail.ToolsByTurnID[turn.ID]
	if len(outcomes) != 1 || !outcomes[0].Success || outcomes[0].ToolName != "shell_execute" {
		t.Fatalf("unexpected persisted tool outcomes: %#v", outcomes)
	}

	exports, err := filepath.Glob(filepath.Join(paths.baseDir, "exports", "chat-*.md"))
	if err != nil || len(exports) != 1 {
		t.Fatalf("expected one chat export, files=%#v err=%v", exports, err)
	}
	info, err := os.Stat(exports[0])
	if err != nil {
		t.Fatalf("stat export: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("export permissions=%#o, want 0600", got)
	}
	exported, err := os.ReadFile(exports[0])
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	for _, expected := range []string{"E2E_TOOL_OK", "Tool-backed response complete.", "Verification: satisfied", "shell_execute: SUCCEEDED"} {
		assertContains(t, string(exported), expected)
	}
}

func TestCommandApprovalRoundTrip(t *testing.T) {
	home := t.TempDir()
	paths := writeConfig(t, home, "http://127.0.0.1:1/v1")
	command := "echo E2E_APPROVED"

	approved, err := runCLI(t, home, "", "commands", "approve", command)
	if err != nil {
		t.Fatalf("commands approve failed: %v\n%s", err, approved)
	}
	assertContains(t, approved, "Approved once (15 minutes, exact action and current policy): echo E2E_APPROVED")

	listed, err := runCLI(t, home, "", "commands", "list")
	if err != nil {
		t.Fatalf("commands list failed: %v\n%s", err, listed)
	}
	assertContains(t, listed, "Scoped one-time grants:")
	assertContains(t, listed, command)
	assertContains(t, listed, "source=cli")
	if _, err := os.Stat(paths.stateDB); err != nil {
		t.Fatalf("approval state DB missing: %v", err)
	}
}

func TestManualApprovalOnceExecutesExactCommandWithoutRemembering(t *testing.T) {
	home := t.TempDir()
	model := newMockModelServer(t)
	defer model.Close()
	paths := writeConfigWithSafety(t, home, model.URL+"/v1", "user_confirm_all")

	output := runChatApprovalDecision(
		t,
		home,
		"run the shell command echo E2E_TOOL_OK then confirm the result",
		"Tool-backed response complete.",
		true,
	)
	for _, expected := range []string{
		"APPROVAL REQUIRED",
		"echo E2E_TOOL_OK",
		"approve once + continue",
		"APPROVED ONCE",
		"E2E_TOOL_OK",
		"Tool-backed response complete.",
	} {
		assertContains(t, output, expected)
	}

	requests := model.Requests()
	if len(requests) != 3 {
		t.Fatalf("model received %d requests, want tool call, final response, and verifier", len(requests))
	}
	toolResult := lastMessage(requests[1])
	if toolResult.Role != "tool" || !strings.Contains(toolResult.Content, "E2E_TOOL_OK") {
		t.Fatalf("approved tool result did not preserve the exact execution result: %#v", toolResult)
	}

	store := state.Open(paths.stateDB)
	defer store.Close()
	approvals, err := store.ListCommandApprovals(context.Background())
	if err != nil {
		t.Fatalf("list command approvals: %v", err)
	}
	if len(approvals) != 0 {
		t.Fatalf("approve-once unexpectedly persisted reusable approvals: %#v", approvals)
	}
	grants, err := store.ListSecurityActionGrants(context.Background())
	if err != nil {
		t.Fatalf("list scoped action grants: %v", err)
	}
	if len(grants) != 1 || grants[0].RemainingUses != 0 || grants[0].UsedAt.IsZero() {
		t.Fatalf("approval continuation did not atomically consume one exact grant: %#v", grants)
	}
}

func TestUnapprovedManualActionNeverExecutes(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "rejected-command-must-not-run")
	command := "touch " + marker
	model := newMockModelServerWithScenario(t, command, "Command was rejected and not executed.")
	defer model.Close()
	paths := writeConfigWithSafety(t, home, model.URL+"/v1", "user_confirm")

	output := runChatApprovalDecision(
		t,
		home,
		"run the shell command "+command+" then confirm whether it ran",
		"Command was rejected and not executed.",
		false,
	)
	for _, expected := range []string{
		"APPROVAL REQUIRED",
		"touch",
		"touch filesystem mutation is ask",
		"approve once + continue",
		"turn canceled before completion verification",
	} {
		assertContains(t, output, expected)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("rejected command created its marker, stat error=%v", err)
	}

	requests := model.Requests()
	if len(requests) != 2 {
		t.Fatalf("model received %d requests, want only classifier plus the original blocked tool proposal", len(requests))
	}
	if len(requests[0].Tools) != 0 || !strings.Contains(lastMessage(requests[0]).Content, "current_user_prompt") {
		t.Fatalf("first request was not the advisory classifier: %#v", requests[0])
	}
	if !toolAdvertised(requests[1], "shell_execute") {
		t.Fatalf("second request was not the original blocked tool proposal: %#v", requests[1])
	}

	store := state.Open(paths.stateDB)
	defer store.Close()
	sessions, err := store.ListRecentChatSessions(context.Background(), 1)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list rejected-command session: sessions=%#v err=%v", sessions, err)
	}
	detail, err := store.GetChatSessionDetail(context.Background(), sessions[0].ID)
	if err != nil || len(detail.Turns) != 1 {
		t.Fatalf("get rejected-command detail: turns=%#v err=%v", detail.Turns, err)
	}
	outcomes := detail.ToolsByTurnID[detail.Turns[0].ID]
	if len(outcomes) != 1 || outcomes[0].Success || !strings.Contains(outcomes[0].ErrorMessage, "approval wait interrupted") {
		t.Fatalf("interrupted approval wait was not persisted as a non-executed tool outcome: %#v", outcomes)
	}
	blocked, err := store.ListBlockedWork(context.Background())
	if err != nil {
		t.Fatalf("list blocked work: %v", err)
	}
	if len(blocked) != 1 || blocked[0].TaskState != state.TaskStateBlockedWaitingUser {
		t.Fatalf("unapproved action did not remain explicitly blocked: %#v", blocked)
	}
}

type configPaths struct {
	baseDir string
	stateDB string
}

func writeConfig(t *testing.T, home, baseURL string) configPaths {
	t.Helper()
	return writeConfigWithSafety(t, home, baseURL, "llm_judge")
}

func writeConfigWithSafety(t *testing.T, home, baseURL, safetyMode string) configPaths {
	t.Helper()
	baseDir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	paths := configPaths{
		baseDir: baseDir,
		stateDB: filepath.Join(baseDir, "state.db"),
	}
	configYAML := fmt.Sprintf(`provider: lmstudio
base_url: %q
default_model: e2e-model
safety_mode: %s
safety_model: e2e-model
max_tokens: 512
max_iterations: 6
log_level: "off"
allowed_commands:
  - echo
routing_enabled: false
memory_dir: %q
state_db_path: %q
prompt_dump_dir: %q
`, baseURL, safetyMode, filepath.Join(baseDir, "memory"), paths.stateDB, filepath.Join(baseDir, "prompt_dumps"))
	if err := os.WriteFile(filepath.Join(baseDir, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return paths
}

func runCLI(t *testing.T, home, input string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, testBinaryPath, args...)
	command.Dir = repositoryRoot
	command.Env = userTestEnv(home)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("command timed out: cvkeharness %s\n%s", strings.Join(args, " "), output)
	}
	return stripANSI(string(output)), err
}

type consoleChatStep struct {
	input   string
	waitFor string
}

func runConsoleChatJourney(t *testing.T, home string, steps []consoleChatStep) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, testBinaryPath, "console", "--view", "chat")
	command.Dir = repositoryRoot
	command.Env = userTestEnv(home)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 36, Cols: 100})
	if err != nil {
		t.Fatalf("start console PTY: %v", err)
	}

	buffer := &lockedBuffer{}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(buffer, terminal)
		close(copyDone)
	}()

	abort := func(message string) {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = terminal.Close()
		t.Fatalf("%s:\n%s", message, stripANSI(buffer.String()))
	}
	if !waitForOutput(buffer, "Ask CvkeHarness", 5*time.Second) {
		abort("console Chat view did not appear")
	}
	for _, step := range steps {
		// Enter focuses the composer when it is blurred and is a no-op when an
		// already-focused empty composer is ready for the next step.
		if _, err := terminal.Write([]byte("\r" + step.input + "\r")); err != nil {
			abort("write console chat input: " + err.Error())
		}
		if !waitForOutput(buffer, step.waitFor, 10*time.Second) {
			abort("console Chat step did not render " + step.waitFor)
		}
	}
	if _, err := terminal.Write([]byte("\x03")); err != nil {
		abort("quit console: " + err.Error())
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("console exited with error: %v\n%s", err, stripANSI(buffer.String()))
		}
	case <-ctx.Done():
		_ = command.Process.Kill()
		t.Fatalf("console did not exit after Ctrl+C:\n%s", stripANSI(buffer.String()))
	}
	_ = terminal.Close()
	select {
	case <-copyDone:
	case <-time.After(time.Second):
	}
	return stripANSI(buffer.String())
}

func runSetupToProviderAndQuit(t *testing.T, home string, width uint16) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, testBinaryPath, "setup")
	command.Dir = repositoryRoot
	command.Env = userTestEnv(home)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 28, Cols: width})
	if err != nil {
		t.Fatalf("start setup PTY: %v", err)
	}

	buffer := &lockedBuffer{}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(buffer, terminal)
		close(copyDone)
	}()

	if !waitForOutput(buffer, "Continue to Connect", 5*time.Second) {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = terminal.Close()
		t.Fatalf("setup welcome screen did not appear at %d columns:\n%s", width, stripANSI(buffer.String()))
	}
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatalf("advance setup: %v", err)
	}
	if !waitForOutput(buffer, "openrouter", 5*time.Second) {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = terminal.Close()
		t.Fatalf("provider screen did not appear at %d columns:\n%s", width, stripANSI(buffer.String()))
	}
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatalf("quit setup: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("setup exited with error at %d columns: %v\n%s", width, err, stripANSI(buffer.String()))
		}
	case <-ctx.Done():
		_ = command.Process.Kill()
		t.Fatalf("setup did not exit after q at %d columns:\n%s", width, stripANSI(buffer.String()))
	}
	_ = terminal.Close()
	select {
	case <-copyDone:
	case <-time.After(time.Second):
	}
	return stripANSI(buffer.String())
}

func runChatApprovalDecision(t *testing.T, home, prompt, finalOutput string, approve bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, testBinaryPath, "console", "--view", "chat")
	command.Dir = repositoryRoot
	command.Env = userTestEnv(home)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 32, Cols: 100})
	if err != nil {
		t.Fatalf("start console Chat PTY: %v", err)
	}

	buffer := &lockedBuffer{}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(buffer, terminal)
		close(copyDone)
	}()

	abort := func(message string) {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = terminal.Close()
		t.Fatalf("%s:\n%s", message, stripANSI(buffer.String()))
	}
	if !waitForOutput(buffer, "Ask CvkeHarness", 5*time.Second) {
		abort("console Chat view did not appear")
	}
	if _, err := terminal.Write([]byte("\r" + prompt + "\r")); err != nil {
		abort("write chat prompt: " + err.Error())
	}
	if !waitForOutput(buffer, "approve once + continue", 5*time.Second) {
		abort("manual approval prompt did not appear")
	}
	if approve {
		if _, err := terminal.Write([]byte("a")); err != nil {
			abort("approve once and continue: " + err.Error())
		}
		if !waitForOutput(buffer, "APPROVED ONCE", 5*time.Second) {
			abort("console did not confirm the exact one-time grant")
		}
		if !waitForOutput(buffer, finalOutput, 10*time.Second) {
			abort("chat did not render the post-approval response")
		}
	} else {
		if _, err := terminal.Write([]byte("\x1b")); err != nil {
			abort("interrupt unapproved turn: " + err.Error())
		}
		if !waitForOutput(buffer, "turn canceled before completion verification", 5*time.Second) {
			abort("console did not interrupt the unapproved turn")
		}
		if !waitForPersistedChatTurn(filepath.Join(home, ".cvkeharness", "state.db"), 5*time.Second) {
			abort("console did not finish persisting the interrupted turn")
		}
	}
	if _, err := terminal.Write([]byte("\x03")); err != nil {
		abort("quit console: " + err.Error())
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("console exited with error after approval decision: %v\n%s", err, stripANSI(buffer.String()))
		}
	case <-ctx.Done():
		_ = command.Process.Kill()
		t.Fatalf("console did not exit after approval decision:\n%s", stripANSI(buffer.String()))
	}
	_ = terminal.Close()
	select {
	case <-copyDone:
	case <-time.After(time.Second):
	}
	return stripANSI(buffer.String())
}

func waitForOutput(buffer *lockedBuffer, expected string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(stripANSI(buffer.String()), expected) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func waitForPersistedChatTurn(stateDB string, timeout time.Duration) bool {
	store := state.Open(stateDB)
	defer store.Close()
	if !store.Available() {
		return false
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sessions, err := store.ListRecentChatSessions(context.Background(), 1)
		if err == nil && len(sessions) == 1 {
			detail, detailErr := store.GetChatSessionDetail(context.Background(), sessions[0].ID)
			if detailErr == nil && len(detail.Turns) == 1 {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type modelRequest struct {
	Model    string             `json:"model"`
	Messages []provider.Message `json:"messages"`
	Tools    []provider.ToolDef `json:"tools"`
}

type mockModelServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []modelRequest
}

func newMockModelServer(t *testing.T) *mockModelServer {
	t.Helper()
	return newMockModelServerWithScenario(t, "echo E2E_TOOL_OK", "Tool-backed response complete.")
}

func newMockModelServerWithScenario(t *testing.T, toolCommand, finalOutput string) *mockModelServer {
	t.Helper()
	model := &mockModelServer{}
	model.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, request)
			return
		}
		var incoming modelRequest
		if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		model.mu.Lock()
		model.requests = append(model.requests, incoming)
		model.mu.Unlock()

		last := lastMessage(incoming)
		var message provider.Message
		switch {
		case strings.Contains(last.Content, "assistant_final_output"):
			message = provider.Message{
				Role:    "assistant",
				Content: `{"status":"satisfied","reason":"The requested echo command ran and its result was reported.","missing_actions":[],"repair_instruction":""}`,
			}
		case last.Role == "tool":
			message = provider.Message{Role: "assistant", Content: finalOutput}
		default:
			message = provider.Message{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:   "call-e2e-shell",
					Type: "function",
					Function: provider.ToolFunction{
						Name:      "shell_execute",
						Arguments: mustJSON(map[string]string{"command": toolCommand}),
					},
				}},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-e2e",
			"model": "e2e-model",
			"choices": []any{map[string]any{
				"message":       message,
				"finish_reason": "stop",
			}},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		})
	}))
	return model
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (m *mockModelServer) Requests() []modelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]modelRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func toolAdvertised(request modelRequest, name string) bool {
	for _, tool := range request.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func lastMessage(request modelRequest) provider.Message {
	if len(request.Messages) == 0 {
		return provider.Message{}
	}
	return request.Messages[len(request.Messages)-1]
}

func userTestEnv(home string) []string {
	return envWith(os.Environ(), map[string]string{
		"HOME":     home,
		"NO_COLOR": "1",
		"TERM":     "dumb",
	})
}

func envWith(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if _, replaced := overrides[key]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func stripANSI(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	return strings.ReplaceAll(value, "\r", "")
}

func assertContains(t *testing.T, value, expected string) {
	t.Helper()
	if !strings.Contains(value, expected) {
		t.Fatalf("output does not contain %q:\n%s", expected, value)
	}
}
