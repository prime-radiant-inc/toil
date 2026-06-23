package engine

import "time"

// durationMs computes the elapsed milliseconds from start to end.
func durationMs(start time.Time, end time.Time) *int64 {
	ms := end.Sub(start).Milliseconds()
	return &ms
}
