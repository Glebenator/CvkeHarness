package scheduler

import (
	"testing"
	"time"
)

func TestNextRunEvery(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	got, err := NextRun(KindEvery, "5m", now)
	if err != nil {
		t.Fatalf("NextRun returned error: %v", err)
	}
	want := now.Add(5 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestNextRunAtPastReturnsZero(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	got, err := NextRun(KindAt, "2026-04-27T09:00:00Z", now)
	if err != nil {
		t.Fatalf("NextRun returned error: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero next run for past one-shot, got %s", got)
	}
}

func TestNextRunCron(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 1, 2, 0, time.UTC)
	got, err := NextRun(KindCron, "*/15 * * * *", now)
	if err != nil {
		t.Fatalf("NextRun returned error: %v", err)
	}
	want := time.Date(2026, 4, 27, 10, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestNextRunCronRejectsMalformedExpression(t *testing.T) {
	_, err := NextRun(KindCron, "* * *", time.Now())
	if err == nil {
		t.Fatal("expected malformed cron expression to fail")
	}
}
