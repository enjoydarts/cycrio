package billing

import (
	"math/big"
	"strings"
)

// Decimal converts a validated nonnegative decimal into exact units of 10^-12.
func Decimal(s string) *big.Int {
	parts := strings.SplitN(s, ".", 2)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	n, _ := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", 12-len(fraction)), 10)
	return n
}
func Add(a, b *big.Int) *big.Int { return new(big.Int).Add(a, b) }
func Sub(a, b *big.Int) *big.Int { return new(big.Int).Sub(a, b) }
func Canonical(n *big.Int) string {
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
	}
	s := new(big.Int).Abs(n).String()
	if len(s) <= 12 {
		s = strings.Repeat("0", 13-len(s)) + s
	}
	fraction := strings.TrimRight(s[len(s)-12:], "0")
	integer := s[:len(s)-12]
	if fraction != "" {
		return sign + integer + "." + fraction
	}
	return sign + integer
}
