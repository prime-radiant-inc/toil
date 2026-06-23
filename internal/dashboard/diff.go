package dashboard

import (
	"strings"
)

// UnifiedDiff returns a line-oriented unified diff of old vs new.
// Each output line is prefixed with " " (context), "-" (removed), or "+" (added).
// Uses an LCS backtrack — O(n*m) in time and space, fine for tool-argument-sized
// strings.
func UnifiedDiff(oldText, newText string) string {
	oldLines := splitLinesKeepEmpty(oldText)
	newLines := splitLinesKeepEmpty(newText)

	n := len(oldLines)
	m := len(newLines)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case oldLines[i] == newLines[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var b strings.Builder
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			b.WriteString(" ")
			b.WriteString(oldLines[i])
			b.WriteString("\n")
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			b.WriteString("-")
			b.WriteString(oldLines[i])
			b.WriteString("\n")
			i++
		default:
			b.WriteString("+")
			b.WriteString(newLines[j])
			b.WriteString("\n")
			j++
		}
	}
	for ; i < n; i++ {
		b.WriteString("-")
		b.WriteString(oldLines[i])
		b.WriteString("\n")
	}
	for ; j < m; j++ {
		b.WriteString("+")
		b.WriteString(newLines[j])
		b.WriteString("\n")
	}
	return b.String()
}

func splitLinesKeepEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
