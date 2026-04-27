package systemcron

import (
	"context"
	"strings"
	"testing"
)

type fakeCrontab struct {
	content string
	writes  []string
	err     error
}

func (f *fakeCrontab) List(context.Context) (string, error) {
	return f.content, f.err
}

func (f *fakeCrontab) Install(_ context.Context, content string) error {
	f.writes = append(f.writes, content)
	f.content = content
	return f.err
}

func TestParsePreservesManagedAndUnmanagedEntries(t *testing.T) {
	content := strings.Join([]string{
		"SHELL=/bin/bash",
		"# human comment",
		"# cvkeharness:id=cron_abc",
		"*/5 * * * * curl -fsS http://localhost:8080/health",
		"# 0 9 * * * echo disabled",
		"",
	}, "\n")
	entries := Parse(content)
	if len(entries) != 2 {
		t.Fatalf("expected two entries, got %#v", entries)
	}
	if !entries[0].Managed || entries[0].ID != "cron_abc" {
		t.Fatalf("expected first entry to be managed, got %#v", entries[0])
	}
	if !entries[1].Disabled {
		t.Fatalf("expected second entry to be disabled, got %#v", entries[1])
	}
}

func TestClientAddUpdateDisableRemove(t *testing.T) {
	fake := &fakeCrontab{content: "SHELL=/bin/bash\n0 1 * * * echo existing\n"}
	client := New(fake)

	add, err := client.Add(context.Background(), "*/5 * * * *", "curl -fsS http://localhost/health", "health")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if !strings.Contains(add.NewContent, "cvkeharness:id=") || !strings.Contains(add.NewContent, "curl -fsS") {
		t.Fatalf("unexpected add content:\n%s", add.NewContent)
	}
	if err := client.Apply(context.Background(), add); err != nil {
		t.Fatalf("Apply add returned error: %v", err)
	}

	update, err := client.Update(context.Background(), add.Target, "*/10 * * * *", "curl -fsS http://localhost/ready")
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if !strings.Contains(update.NewContent, "*/10 * * * * curl -fsS http://localhost/ready") {
		t.Fatalf("unexpected update content:\n%s", update.NewContent)
	}
	if err := client.Apply(context.Background(), update); err != nil {
		t.Fatalf("Apply update returned error: %v", err)
	}

	disable, err := client.SetEnabled(context.Background(), add.Target, false)
	if err != nil {
		t.Fatalf("SetEnabled false returned error: %v", err)
	}
	if !strings.Contains(disable.NewContent, "# */10 * * * * curl -fsS") {
		t.Fatalf("unexpected disable content:\n%s", disable.NewContent)
	}
	if err := client.Apply(context.Background(), disable); err != nil {
		t.Fatalf("Apply disable returned error: %v", err)
	}

	remove, err := client.Remove(context.Background(), add.Target)
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if strings.Contains(remove.NewContent, "localhost/ready") {
		t.Fatalf("expected removed job, got:\n%s", remove.NewContent)
	}
	if strings.Contains(remove.NewContent, add.Target) {
		t.Fatalf("expected managed metadata to be removed, got:\n%s", remove.NewContent)
	}
}

func TestValidateEntryRejectsUnsafeOrMalformedInput(t *testing.T) {
	if err := ValidateEntry("*/5 * * * *", "echo ok"); err != nil {
		t.Fatalf("expected valid entry: %v", err)
	}
	if err := ValidateEntry("* * *", "echo bad"); err == nil {
		t.Fatal("expected malformed schedule to fail")
	}
	if err := ValidateEntry("* * * * *", "echo ok\nrm -rf /"); err == nil {
		t.Fatal("expected multiline command to fail")
	}
}
