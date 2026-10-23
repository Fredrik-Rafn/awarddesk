package eva

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

func partyCashAmount(raw string, adults int) (string, error) {
	if adults < 1 {
		adults = 1
	}
	if len(raw) > 64 || !json.Valid([]byte(raw)) || adults > 9 {
		return "", errors.New("invalid EVA cash amount")
	}
	mantissa, exponent := raw, 0
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		var err error
		exponent, err = strconv.Atoi(raw[i+1:])
		if err != nil || exponent < -64 || exponent > 64 {
			return "", errors.New("invalid EVA cash scale")
		}
		mantissa = raw[:i]
	}
	scale := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		scale = len(mantissa) - i - 1
	}
	scale -= exponent
	if scale < 0 {
		scale = 0
	}
	if scale > 64 {
		return "", errors.New("invalid EVA cash scale")
	}
	amount, ok := new(big.Rat).SetString(raw)
	if !ok || amount.Sign() < 0 {
		return "", errors.New("invalid EVA cash amount")
	}
	if adults == 1 {
		return raw, nil
	}
	amount.Mul(amount, new(big.Rat).SetInt64(int64(adults)))
	return amount.FloatString(scale), nil
}

func equalCashAmounts(a, b string) bool {
	if _, err := partyCashAmount(a, 1); err != nil {
		return false
	}
	if _, err := partyCashAmount(b, 1); err != nil {
		return false
	}
	x, ok := new(big.Rat).SetString(a)
	y, otherOK := new(big.Rat).SetString(b)
	return ok && otherOK && x.Cmp(y) == 0
}
