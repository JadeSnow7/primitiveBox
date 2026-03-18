package calc

import (
	"testing"
)

func TestAdd(t *testing.T) {
	got := Add(2, 3)
	if got != 5 {
		t.Errorf("Add(2,3) = %v, want 5", got)
	}
}

func TestAddNegative(t *testing.T) {
	got := Add(-1, -2)
	if got != -3 {
		t.Errorf("Add(-1,-2) = %v, want -3", got)
	}
}

func TestDivide(t *testing.T) {
	got, err := Divide(10, 2)
	if err != nil || got != 5 {
		t.Errorf("Divide(10,2) = %v, %v; want 5, nil", got, err)
	}
}

func TestDivideByZero(t *testing.T) {
	_, err := Divide(10, 0)
	if err == nil {
		t.Error("Divide(10,0) should return error, got nil")
	}
}

func TestSqrt(t *testing.T) {
	got, err := Sqrt(16)
	if err != nil || got != 4 {
		t.Errorf("Sqrt(16) = %v, %v; want 4, nil", got, err)
	}
}

func TestSqrtNegative(t *testing.T) {
	_, err := Sqrt(-1)
	if err == nil {
		t.Error("Sqrt(-1) should return error, got nil")
	}
}
