package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KindAt    = "at"
	KindEvery = "every"
	KindCron  = "cron"
)

// NextRun returns the first run time after now for a supported schedule.
func NextRun(kind, spec string, now time.Time) (time.Time, error) {
	now = now.UTC()
	switch strings.TrimSpace(kind) {
	case KindAt:
		runAt, err := time.Parse(time.RFC3339, strings.TrimSpace(spec))
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid at schedule: %w", err)
		}
		if !runAt.After(now) {
			return time.Time{}, nil
		}
		return runAt.UTC(), nil
	case KindEvery:
		d, err := time.ParseDuration(strings.TrimSpace(spec))
		if err != nil || d <= 0 {
			return time.Time{}, fmt.Errorf("invalid every schedule %q", spec)
		}
		return now.Add(d), nil
	case KindCron:
		expr, err := parseCronExpr(spec)
		if err != nil {
			return time.Time{}, err
		}
		cursor := now.Truncate(time.Minute).Add(time.Minute)
		limit := cursor.Add(366 * 24 * time.Hour)
		for cursor.Before(limit) {
			if expr.matches(cursor) {
				return cursor.UTC(), nil
			}
			cursor = cursor.Add(time.Minute)
		}
		return time.Time{}, fmt.Errorf("cron schedule did not resolve within one year")
	default:
		return time.Time{}, fmt.Errorf("unsupported schedule kind %q", kind)
	}
}

type cronExpr struct {
	minute cronField
	hour   cronField
	dom    cronField
	month  cronField
	dow    cronField
}

type cronField struct {
	any    bool
	values map[int]bool
}

func parseCronExpr(spec string) (cronExpr, error) {
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return cronExpr{}, fmt.Errorf("cron schedule must have 5 fields")
	}
	fields := []struct {
		name     string
		min, max int
		raw      string
	}{
		{"minute", 0, 59, parts[0]},
		{"hour", 0, 23, parts[1]},
		{"day-of-month", 1, 31, parts[2]},
		{"month", 1, 12, parts[3]},
		{"day-of-week", 0, 7, parts[4]},
	}
	parsed := make([]cronField, len(fields))
	for i, field := range fields {
		value, err := parseCronField(field.raw, field.min, field.max)
		if err != nil {
			return cronExpr{}, fmt.Errorf("invalid %s field: %w", field.name, err)
		}
		parsed[i] = value
	}
	return cronExpr{
		minute: parsed[0],
		hour:   parsed[1],
		dom:    parsed[2],
		month:  parsed[3],
		dow:    parsed[4],
	}, nil
}

func parseCronField(raw string, minValue, maxValue int) (cronField, error) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return cronField{any: true}, nil
	}
	out := cronField{values: make(map[int]bool)}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return cronField{}, fmt.Errorf("empty list item")
		}
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return cronField{}, fmt.Errorf("malformed step %q", part)
			}
			base = pieces[0]
			n, err := strconv.Atoi(pieces[1])
			if err != nil || n <= 0 {
				return cronField{}, fmt.Errorf("invalid step %q", pieces[1])
			}
			step = n
		}
		start, end, err := parseCronRange(base, minValue, maxValue)
		if err != nil {
			return cronField{}, err
		}
		for n := start; n <= end; n += step {
			out.values[n] = true
			if maxValue == 7 && n == 7 {
				out.values[0] = true
			}
		}
	}
	return out, nil
}

func parseCronRange(raw string, minValue, maxValue int) (int, int, error) {
	if raw == "*" {
		return minValue, maxValue, nil
	}
	if strings.Contains(raw, "-") {
		pieces := strings.Split(raw, "-")
		if len(pieces) != 2 {
			return 0, 0, fmt.Errorf("malformed range %q", raw)
		}
		start, err := strconv.Atoi(pieces[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid number %q", pieces[0])
		}
		end, err := strconv.Atoi(pieces[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid number %q", pieces[1])
		}
		if start > end {
			return 0, 0, fmt.Errorf("range start exceeds end")
		}
		if start < minValue || end > maxValue {
			return 0, 0, fmt.Errorf("range outside %d-%d", minValue, maxValue)
		}
		return start, end, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid number %q", raw)
	}
	if n < minValue || n > maxValue {
		return 0, 0, fmt.Errorf("value outside %d-%d", minValue, maxValue)
	}
	return n, n, nil
}

func (e cronExpr) matches(t time.Time) bool {
	return e.minute.matches(t.Minute()) &&
		e.hour.matches(t.Hour()) &&
		e.dom.matches(t.Day()) &&
		e.month.matches(int(t.Month())) &&
		e.dow.matches(int(t.Weekday()))
}

func (f cronField) matches(n int) bool {
	if f.any {
		return true
	}
	return f.values[n]
}
