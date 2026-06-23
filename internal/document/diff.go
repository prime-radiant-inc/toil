package document

import "strings"

// UnifiedDiff produces unified-diff hunks comparing before to after.
// context is the number of unchanged lines of context to include around each change.
// Returns no hunks when the two strings are identical.
func UnifiedDiff(before, after string, context int) []DiffHunk {
	a := splitLinesKeepingEmpty(before)
	b := splitLinesKeepingEmpty(after)
	ops := myersDiff(a, b)
	return groupHunks(a, b, ops, context)
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	aIdx int  // 1-based source line for ' ' and '-'
	bIdx int  // 1-based source line for ' ' and '+'
}

// splitLinesKeepingEmpty splits on '\n' and drops a trailing empty element
// that would otherwise appear when the input ends in '\n'.
func splitLinesKeepingEmpty(s string) []string {
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}

// myersDiff returns a sequence of ops comparing a→b using Myers's
// shortest-edit-script algorithm. Output is in (a,b) order.
func myersDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	v := make(map[int]int, 2*max+1)
	var trace []map[int]int
	var d int
findD:
	for d = 0; d <= max; d++ {
		snapshot := make(map[int]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[k] = x
			snapshot[k] = x
			if x >= n && y >= m {
				trace = append(trace, snapshot)
				break findD
			}
		}
		trace = append(trace, snapshot)
	}
	var ops []diffOp
	x, y := n, m
	for di := d; di > 0; di-- {
		vd := trace[di]
		k := x - y
		var prevK int
		if k == -di || (k != di && vd[k-1] < vd[k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := trace[di-1][prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append([]diffOp{{kind: ' ', aIdx: x, bIdx: y}}, ops...)
			x--
			y--
		}
		if x == prevX {
			ops = append([]diffOp{{kind: '+', bIdx: y}}, ops...)
		} else {
			ops = append([]diffOp{{kind: '-', aIdx: x}}, ops...)
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 {
		ops = append([]diffOp{{kind: ' ', aIdx: x, bIdx: y}}, ops...)
		x--
		y--
	}
	return ops
}

func groupHunks(a, b []string, ops []diffOp, context int) []DiffHunk {
	var hunks []DiffHunk
	var cur *DiffHunk
	var ctxQueue []diffOp
	flushCtxQueue := func() {
		if cur == nil {
			return
		}
		for _, op := range ctxQueue {
			appendOpToHunk(cur, a, b, op)
		}
		ctxQueue = nil
	}
	for _, op := range ops {
		if op.kind == ' ' {
			if cur == nil {
				ctxQueue = append(ctxQueue, op)
				if len(ctxQueue) > context {
					ctxQueue = ctxQueue[1:]
				}
				continue
			}
			ctxQueue = append(ctxQueue, op)
			if len(ctxQueue) > 2*context {
				// Close hunk with leading context.
				for i := 0; i < context; i++ {
					appendOpToHunk(cur, a, b, ctxQueue[i])
				}
				hunks = append(hunks, *cur)
				cur = nil
				ctxQueue = ctxQueue[len(ctxQueue)-context:]
			}
		} else {
			if cur == nil {
				cur = &DiffHunk{}
				for _, q := range ctxQueue {
					if cur.OldStart == 0 {
						cur.OldStart = q.aIdx
						cur.NewStart = q.bIdx
					}
					appendOpToHunk(cur, a, b, q)
				}
				ctxQueue = nil
			} else {
				flushCtxQueue()
			}
			if cur.OldStart == 0 {
				if op.aIdx > 0 {
					cur.OldStart = op.aIdx
				} else {
					cur.OldStart = 1
				}
				if op.bIdx > 0 {
					cur.NewStart = op.bIdx
				} else {
					cur.NewStart = 1
				}
			}
			appendOpToHunk(cur, a, b, op)
		}
	}
	if cur != nil {
		for i := 0; i < len(ctxQueue) && i < context; i++ {
			appendOpToHunk(cur, a, b, ctxQueue[i])
		}
		hunks = append(hunks, *cur)
	}
	return hunks
}

func appendOpToHunk(h *DiffHunk, a, b []string, op diffOp) {
	switch op.kind {
	case ' ':
		h.OldLines++
		h.NewLines++
		h.Lines = append(h.Lines, " "+a[op.aIdx-1])
	case '-':
		h.OldLines++
		h.Lines = append(h.Lines, "-"+a[op.aIdx-1])
	case '+':
		h.NewLines++
		h.Lines = append(h.Lines, "+"+b[op.bIdx-1])
	}
}
