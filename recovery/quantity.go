package recovery

import (
	"fmt"
	"math/big"
	"strings"
)

// Quantity values are decimal strings, never binary floating point. Units are
// explicit and case-sensitive: MB and MiB have different meanings.
type Quantity struct {
	Value string `json:"value"`
	Unit  string `json:"unit"`
}

type Calculation struct {
	Operation  string   `json:"operation"`
	Left       Quantity `json:"left"`
	Right      Quantity `json:"right"`
	OutputUnit string   `json:"output_unit"`
	Rounding   string   `json:"rounding"` // exact, floor, ceil, nearest_even
	Expected   string   `json:"expected,omitempty"`
}

type CalculationResult struct {
	Exact           string `json:"exact"` // exact rational in output units
	Value           string `json:"value"` // integer after explicit rounding
	Unit            string `json:"unit"`
	Rounding        string `json:"rounding"`
	ExpectedMatches *bool  `json:"expected_matches,omitempty"`
}

type quantityUnit struct {
	dimension              string
	numerator, denominator int64
}

func unitDefinition(unit string) (quantityUnit, error) {
	units := map[string]quantityUnit{
		"count": {"scalar", 1, 1}, "%": {"scalar", 1, 100},
		"B": {"bytes", 1, 1}, "kB": {"bytes", 1000, 1}, "MB": {"bytes", 1000000, 1}, "GB": {"bytes", 1000000000, 1},
		"KiB": {"bytes", 1 << 10, 1}, "MiB": {"bytes", 1 << 20, 1}, "GiB": {"bytes", 1 << 30, 1}, "TiB": {"bytes", 1 << 40, 1},
		"ms": {"time", 1, 1000}, "s": {"time", 1, 1}, "min": {"time", 60, 1}, "h": {"time", 3600, 1},
	}
	u, ok := units[unit]
	if !ok {
		return u, fmt.Errorf("unknown or ambiguous unit %q", unit)
	}
	return u, nil
}

func decimal(value string) (*big.Rat, error) {
	if len(value) == 0 || len(value) > 64 || strings.Trim(value, "-0123456789.") != "" || strings.Count(value, ".") > 1 || strings.Count(value, "-") > 1 || strings.Contains(value[1:], "-") {
		return nil, fmt.Errorf("value must be a decimal string of at most 64 characters, without exponents")
	}
	text := strings.TrimPrefix(value, "-")
	if text == "" || strings.HasPrefix(text, ".") || strings.HasSuffix(text, ".") {
		return nil, fmt.Errorf("decimal requires digits on both sides of a decimal point")
	}
	parts := strings.SplitN(text, ".", 2)
	digits, scale := parts[0], 0
	if len(parts) == 2 {
		digits += parts[1]
		scale = len(parts[1])
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal value")
	}
	if strings.HasPrefix(value, "-") {
		n.Neg(n)
	}
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	return new(big.Rat).SetFrac(n, denominator), nil
}

func quantityBase(q Quantity) (*big.Rat, string, error) {
	x, err := decimal(q.Value)
	if err != nil {
		return nil, "", err
	}
	u, err := unitDefinition(q.Unit)
	if err != nil {
		return nil, "", err
	}
	x.Mul(x, big.NewRat(u.numerator, u.denominator))
	return x, u.dimension, nil
}

// Calculate performs two-operand dimensional arithmetic. Compound units are
// deliberately unsupported; multiplication/division may scale a quantity by a
// scalar, and division of equal dimensions yields a scalar. The output has an
// explicit integer rounding policy, suitable for executor resource limits.
func Calculate(c Calculation) (CalculationResult, error) {
	a, ad, err := quantityBase(c.Left)
	if err != nil {
		return CalculationResult{}, err
	}
	b, bd, err := quantityBase(c.Right)
	if err != nil {
		return CalculationResult{}, err
	}
	x, dimension := new(big.Rat), ad
	switch c.Operation {
	case "add", "subtract":
		if ad != bd {
			return CalculationResult{}, fmt.Errorf("addition/subtraction requires matching dimensions")
		}
		if c.Operation == "add" {
			x.Add(a, b)
		} else {
			x.Sub(a, b)
		}
	case "multiply":
		if ad != "scalar" && bd != "scalar" {
			return CalculationResult{}, fmt.Errorf("compound dimensions are unsupported")
		}
		if ad == "scalar" {
			dimension = bd
		}
		x.Mul(a, b)
	case "divide":
		if b.Sign() == 0 {
			return CalculationResult{}, fmt.Errorf("division by zero")
		}
		if ad == bd {
			dimension = "scalar"
		} else if bd != "scalar" {
			return CalculationResult{}, fmt.Errorf("unsupported dimension division")
		}
		x.Quo(a, b)
	default:
		return CalculationResult{}, fmt.Errorf("operation must be add, subtract, multiply or divide")
	}
	u, err := unitDefinition(c.OutputUnit)
	if err != nil {
		return CalculationResult{}, err
	}
	if dimension != u.dimension {
		return CalculationResult{}, fmt.Errorf("output unit has the wrong dimension")
	}
	x.Quo(x, big.NewRat(u.numerator, u.denominator))
	out := CalculationResult{Exact: x.RatString(), Unit: c.OutputUnit, Rounding: c.Rounding}
	integer, err := roundRational(x, c.Rounding)
	if err != nil {
		return out, err
	}
	if !integer.IsInt64() {
		return out, fmt.Errorf("result exceeds signed 64-bit executor range")
	}
	out.Value = integer.String()
	if c.Expected != "" {
		expected, err := decimal(c.Expected)
		if err != nil {
			return out, err
		}
		match := expected.Cmp(new(big.Rat).SetInt(integer)) == 0
		out.ExpectedMatches = &match
		if !match {
			return out, fmt.Errorf("supplied expected result does not match checked arithmetic")
		}
	}
	return out, nil
}

func roundRational(x *big.Rat, mode string) (*big.Int, error) {
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(x.Num(), x.Denom(), rem)
	sign := int64(x.Sign())
	switch mode {
	case "exact":
		if rem.Sign() != 0 {
			return nil, fmt.Errorf("fractional result requires an explicit rounding policy")
		}
	case "floor":
		if rem.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		}
	case "ceil":
		if rem.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
	case "nearest_even":
		twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
		cmp := twice.Cmp(x.Denom())
		if cmp > 0 || cmp == 0 && q.Bit(0) != 0 {
			q.Add(q, big.NewInt(sign))
		}
	default:
		return nil, fmt.Errorf("rounding must be exact, floor, ceil or nearest_even")
	}
	return q, nil
}

func checkedProduct(a, b uint64) (int64, error) {
	x := new(big.Int).Mul(new(big.Int).SetUint64(a), new(big.Int).SetUint64(b))
	if !x.IsInt64() {
		return 0, fmt.Errorf("resource measurement multiplication exceeds signed 64-bit range")
	}
	return x.Int64(), nil
}
