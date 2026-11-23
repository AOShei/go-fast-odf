// Package num parses the numbers of attributes, most of which are absent:
// an absent or empty one fails without the error value strconv makes
// for each failure.
package num

import (
	"errors"
	"strconv"
)

// ErrEmpty is the error for an empty string.
var ErrEmpty = errors.New("num: empty")

// Atoi is strconv.Atoi, but for "" it returns ErrEmpty, which costs
// nothing to return.
func Atoi(s string) (int, error) {
	if s == "" {
		return 0, ErrEmpty
	}
	return strconv.Atoi(s)
}

// ParseInt is strconv.ParseInt, but for "" it returns ErrEmpty.
func ParseInt(s string, base, bitSize int) (int64, error) {
	if s == "" {
		return 0, ErrEmpty
	}
	return strconv.ParseInt(s, base, bitSize)
}
