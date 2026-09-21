package recovery

import (
	"math"
	"testing"
)

func TestCheckedQuantityArithmetic(t *testing.T) {
	for _, tc := range []struct{ name, operation, left, lu, right, ru, output, round, want string }{
		{"percentage", "multiply", "75", "%", "8", "GiB", "MiB", "exact", "6144"},
		{"decimal exactness", "add", "0.1", "count", "0.2", "count", "%", "exact", "30"},
		{"binary vs decimal", "subtract", "1", "MiB", "1", "MB", "B", "exact", "48576"},
		{"time", "divide", "1.5", "min", "2", "count", "s", "exact", "45"},
		{"ratio", "divide", "1", "GiB", "512", "MiB", "count", "exact", "2"},
		{"decimal leading zeros", "add", "010", "count", "009", "count", "count", "exact", "19"},
		{"floor negative", "divide", "-5", "count", "2", "count", "count", "floor", "-3"},
		{"ceil negative", "divide", "-5", "count", "2", "count", "count", "ceil", "-2"},
		{"ties even positive", "divide", "5", "count", "2", "count", "count", "nearest_even", "2"},
		{"ties even negative", "divide", "-7", "count", "2", "count", "count", "nearest_even", "-4"},
		{"ceil allocation", "divide", "4097", "B", "1", "count", "KiB", "ceil", "5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Calculation{Operation: tc.operation, Left: Quantity{tc.left, tc.lu}, Right: Quantity{tc.right, tc.ru}, OutputUnit: tc.output, Rounding: tc.round, Expected: tc.want}
			got, err := Calculate(c)
			if err != nil || got.Value != tc.want || got.ExpectedMatches == nil || !*got.ExpectedMatches {
				t.Fatalf("got %#v / %v, want %s", got, err, tc.want)
			}
		})
	}
}

func TestCheckedArithmeticRefusesInvalidInputs(t *testing.T) {
	base := Calculation{Operation: "divide", Left: Quantity{"5", "B"}, Right: Quantity{"2", "count"}, OutputUnit: "B", Rounding: "exact"}
	for name, edit := range map[string]func(*Calculation){
		"fraction needs rounding": func(c *Calculation) {},
		"zero divisor":            func(c *Calculation) { c.Right.Value = "0" },
		"ambiguous unit":          func(c *Calculation) { c.Left.Unit = "mb" },
		"wrong dimension":         func(c *Calculation) { c.OutputUnit = "s" },
		"nonfinite":               func(c *Calculation) { c.Left.Value = "NaN" },
		"exponent":                func(c *Calculation) { c.Left.Value = "1e3" },
		"implicit rounding":       func(c *Calculation) { c.Rounding = "" },
		"overflow":                func(c *Calculation) { c.Left.Value = "18446744073709551616"; c.Right.Value = "1" },
		"incorrect math":          func(c *Calculation) { c.Rounding = "ceil"; c.Expected = "2" },
		"compound units":          func(c *Calculation) { c.Operation = "multiply"; c.Right.Unit = "s" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			edit(&c)
			if _, err := Calculate(c); err == nil {
				t.Fatal("invalid numeric input accepted")
			}
		})
	}
	if _, err := checkedProduct(math.MaxUint64, 2); err == nil {
		t.Fatal("unsigned measurement overflow accepted")
	}
}

func FuzzDecimalRounding(f *testing.F) {
	f.Add("-2.5", "nearest_even")
	f.Add("010", "exact")
	f.Fuzz(func(t *testing.T, value, mode string) {
		c := Calculation{Operation: "multiply", Left: Quantity{value, "count"}, Right: Quantity{"1", "count"}, OutputUnit: "count", Rounding: mode}
		_, _ = Calculate(c)
	})
}
