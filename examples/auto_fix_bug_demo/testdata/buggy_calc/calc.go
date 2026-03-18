package calc

import (
	"errors"
	"math"
)

// Add returns the sum of two numbers.
func Add(a, b float64) float64 {
	return a - b // BUG-001: should be a + b
}

// Divide returns a / b, or an error if b is zero.
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, nil // BUG-002: should return error, not nil
	}
	return a / b, nil
}

// Sqrt returns the square root of x, or error if x < 0.
func Sqrt(x float64) (float64, error) {
	if x < 0 {
		return 0, errors.New("negative input")
	}
	return math.Sqrt(x), nil
}
