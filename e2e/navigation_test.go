//go:build e2e && !windows

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestWorkspaceFocusAndSettingsSidebarThroughPTY(t *testing.T) {
	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d_columns", width), func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, testBinaryPath, "settings")
			command.Env = envWith(userTestEnv(home), map[string]string{"CODEX_HOME": filepath.Join(home, ".codex")})
			terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: width})
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			buffer := &lockedBuffer{}
			go func() { _, _ = io.Copy(buffer, terminal) }()
			step := func(input, want string) {
				t.Helper()
				start := len(buffer.String())
				if input == "" {
					start = 0
				} else if _, err := terminal.Write([]byte(input)); err != nil {
					t.Fatal(err)
				}
				if !waitForOutputAfter(buffer, start, want, 5*time.Second) {
					t.Fatalf("missing %q after %q:\n%s", want, input, stripANSI(buffer.String()))
				}
			}
			step("", "Configuration only")
			step("\x1b[D", "→/enter content")
			step("\x1b[B", "Save provider access once")
			step("\x1b[C", "← sections")
			step("\r", "Edit connection")
			step("\x1b", "Save provider access once")
			step("\x1b", "select tab")
			step("\x1b[D", "Ask CvkeHarness")
			step("\x1b[C", "Save provider access once")
			start := len(buffer.String())
			// a must be ignored until Enter focuses Settings. That Enter must
			// not also open the selected connection editor.
			step("a\r", "← sections")
			after := stripANSI(buffer.String()[start:])
			if strings.Contains(after, "New connection") || strings.Contains(after, "Edit connection") {
				t.Fatal("top-bar selection leaked into a workspace action")
			}
			step("a", "New connection")
			if _, err := terminal.Write([]byte("\x03")); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"config.yaml", "state.db"} {
				if _, err := os.Stat(filepath.Join(home, ".cvkeharness", name)); !os.IsNotExist(err) {
					t.Fatalf("navigation created %s", name)
				}
			}
		})
	}
}
