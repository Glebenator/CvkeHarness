package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/coolcake/cvkeharness/recovery"
)

type CalculateTool struct{}

func (t *CalculateTool) Name() string { return "safety_calculate" }
func (t *CalculateTool) Description() string {
	return "Check arithmetic for resource decisions using exact decimal strings, explicit units and integer rounding. Supports add/subtract of matching dimensions, scalar multiplication/division, and ratios of equal dimensions. Units: count, %, B/kB/MB/GB, KiB/MiB/GiB/TiB, ms/s/min/h. Rounding: exact, floor, ceil, nearest_even. Supply expected to detect a mistaken result. Pure arithmetic only: this does not measure a target or authorize execution; supported recovery executors enforce their own fresh capacity checks."
}
func (t *CalculateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string","enum":["add","subtract","multiply","divide"]},"left":{"type":"object","properties":{"value":{"type":"string"},"unit":{"type":"string"}},"required":["value","unit"],"additionalProperties":false},"right":{"type":"object","properties":{"value":{"type":"string"},"unit":{"type":"string"}},"required":["value","unit"],"additionalProperties":false},"output_unit":{"type":"string"},"rounding":{"type":"string","enum":["exact","floor","ceil","nearest_even"]},"expected":{"type":"string"}},"required":["operation","left","right","output_unit","rounding"],"additionalProperties":false}`)
}
func (t *CalculateTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(raw) > 4096 {
		return "", fmt.Errorf("calculation request exceeds 4 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var request recovery.Calculation
	if err := d.Decode(&request); err != nil {
		return "", err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("trailing calculation arguments")
	}
	out, err := recovery.Calculate(request)
	b, _ := json.Marshal(out)
	return string(b), err
}
