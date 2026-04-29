package payment

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

var zeroDecimalCurrencies = map[string]struct{}{
	"BIF": {},
	"CLP": {},
	"DJF": {},
	"GNF": {},
	"JPY": {},
	"KMF": {},
	"KRW": {},
	"MGA": {},
	"PYG": {},
	"RWF": {},
	"UGX": {},
	"VND": {},
	"VUV": {},
	"XAF": {},
	"XOF": {},
	"XPF": {},
}

// CurrencyMinorUnits returns the number of fractional decimal places used by a
// currency for provider minor-unit APIs. Unknown currencies default to 2.
func CurrencyMinorUnits(currency string) int32 {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "":
		return 2
	case "BHD", "JOD", "KWD", "OMR", "TND":
		return 3
	default:
		if _, ok := zeroDecimalCurrencies[strings.ToUpper(strings.TrimSpace(currency))]; ok {
			return 0
		}
		return 2
	}
}

// AmountToMinorUnits converts a decimal amount string in the specified currency
// to provider minor units using currency-specific precision.
func AmountToMinorUnits(amountStr, currency string) (int64, error) {
	d, err := decimal.NewFromString(strings.TrimSpace(amountStr))
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", amountStr)
	}
	if d.IsNegative() {
		return 0, fmt.Errorf("invalid negative amount: %s", amountStr)
	}
	factor := decimal.NewFromInt(10).Pow(decimal.NewFromInt32(CurrencyMinorUnits(currency)))
	return d.Mul(factor).Round(0).IntPart(), nil
}

// MinorUnitsToAmount converts provider minor units back to a decimal float for
// provider query/webhook interfaces.
func MinorUnitsToAmount(amount int64, currency string) float64 {
	factor := decimal.NewFromInt(10).Pow(decimal.NewFromInt32(CurrencyMinorUnits(currency)))
	return decimal.NewFromInt(amount).Div(factor).InexactFloat64()
}

// YuanToFen converts a CNY yuan string (e.g. "10.50") to fen (int64).
// Uses shopspring/decimal for precision.
func YuanToFen(yuanStr string) (int64, error) {
	return AmountToMinorUnits(yuanStr, "CNY")
}

// FenToYuan converts fen (int64) to yuan as a float64 for interface compatibility.
func FenToYuan(fen int64) float64 {
	return MinorUnitsToAmount(fen, "CNY")
}
