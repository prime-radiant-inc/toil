package metrics

import "fmt"

// FormatDuration renders a millisecond count as "34s", "2m 18s", or
// "1h 04m". Seconds are dropped past an hour.
func FormatDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
	}
	// Convert to total minutes (ceiling), then split into hours and remaining minutes.
	totalMins := (seconds + 59) / 60
	hours := totalMins / 60
	mins := totalMins % 60
	return fmt.Sprintf("%dh %02dm", hours, mins)
}

// FormatTokens renders a token count with k/M compaction above 1000,
// keeping 3 significant figures.
func FormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return trimTrailingZero(fmt.Sprintf("%.2f", float64(n)/1000.0)) + "k"
	}
	return trimTrailingZero(fmt.Sprintf("%.2f", float64(n)/1_000_000.0)) + "M"
}

// trimTrailingZero trims the last fractional digit when the integer part is
// two or more digits, converting "12.40" to "12.4" while leaving "1.00"
// intact. Input is always in "N.NN" form (two decimal places).
func trimTrailingZero(s string) string {
	dot := -1
	for i, ch := range s {
		if ch == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return s
	}
	// Keep only one decimal place when the integer part has 2+ digits.
	if dot >= 2 {
		return s[:dot+2]
	}
	return s
}

// FormatCost renders a cost value as a user-facing label.
//
// Distinguishes three states:
//   - nil           → "—"       (no cost recorded: unknown model)
//   - &0            → "$0.00"   (priced, but zero tokens)
//   - non-zero      → "$X.YY…"  (formatted per magnitude)
//
// Rules for non-zero:
//   - < $0.001   → "$0.0001" (4 decimals, avoids "$0.00" for tiny values)
//   - < $0.9995  → "$0.043"  (3 decimals, re-checked against $1 boundary)
//   - ≥ $0.9995  → "$12.40"  (2 decimals; includes values that round up to $1)
func FormatCost(p *float64) string {
	if p == nil {
		return "—"
	}
	f := *p
	if f == 0 {
		return "$0.00"
	}
	if f < 0.001 {
		return fmt.Sprintf("$%.4f", f)
	}
	// At $0.9995 and above, rounding to 3dp would cross into $1; drop to 2dp.
	if f >= 0.9995 {
		return fmt.Sprintf("$%.2f", f)
	}
	return fmt.Sprintf("$%.3f", f)
}
