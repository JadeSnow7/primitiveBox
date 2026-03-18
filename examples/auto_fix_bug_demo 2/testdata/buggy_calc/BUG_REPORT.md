# Bug Report

## BUG-001: Add returns wrong result
- Severity: High
- Repro: `Add(2, 3)` returns `-1` instead of `5`
- Expected: Addition should return the sum

## BUG-002: Divide by zero does not return error
- Severity: High
- Repro: `Divide(10, 0)` returns `(0, nil)` instead of an error
- Expected: Division by zero should return a non-nil error
