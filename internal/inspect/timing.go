package inspect

import (
	"sort"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("timing", func(rs *state.RunState) Processor { return NewTimingProcessor(rs) })
}

type TimingResult struct {
	TotalS      float64      `json:"total_s"`
	Nodes       []NodeTiming `json:"nodes"`
	Bottlenecks []Bottleneck `json:"bottlenecks"`
}

type NodeTiming struct {
	ID             string   `json:"id"`
	DurationS      float64  `json:"duration_s"`
	Pct            float64  `json:"pct"`
	ConcurrentWith []string `json:"concurrent_with,omitempty"`
}

type Bottleneck struct {
	Node      string  `json:"node"`
	Reason    string  `json:"reason"`
	DurationS float64 `json:"duration_s"`
}

type timingProcessor struct {
	rs *state.RunState
}

func NewTimingProcessor(rs *state.RunState) *timingProcessor {
	return &timingProcessor{rs: rs}
}

func (p *timingProcessor) ProcessEvent(event state.Event) {
	// Timing is computed from RunState, not events.
}

func (p *timingProcessor) Changed() bool {
	return false
}

func (p *timingProcessor) Result() any {
	var totalS float64
	if p.rs.FinishedAt != nil {
		totalS = p.rs.FinishedAt.Sub(p.rs.StartedAt).Seconds()
	}

	type nodeWindow struct {
		id    string
		start int64 // unix nano
		end   int64
		durS  float64
	}

	var windows []nodeWindow

	p.rs.WithNodes(func(nodes map[string]*state.NodeState) {
		for _, n := range nodes {
			if n.StartedAt == nil || n.EndedAt == nil {
				continue
			}
			durS := n.EndedAt.Sub(*n.StartedAt).Seconds()
			windows = append(windows, nodeWindow{
				id:    n.ID,
				start: n.StartedAt.UnixNano(),
				end:   n.EndedAt.UnixNano(),
				durS:  durS,
			})
		}
	})

	// Sort by start time, then by ID for determinism
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].start != windows[j].start {
			return windows[i].start < windows[j].start
		}
		return windows[i].id < windows[j].id
	})

	// Build node timings with concurrency detection
	nodeTimes := make([]NodeTiming, len(windows))
	for i, w := range windows {
		pct := 0.0
		if totalS > 0 {
			pct = (w.durS / totalS) * 100.0
		}

		var concurrent []string
		for j, other := range windows {
			if i == j {
				continue
			}
			// Overlap: w.start < other.end && other.start < w.end
			if w.start < other.end && other.start < w.end {
				concurrent = append(concurrent, other.id)
			}
		}

		nodeTimes[i] = NodeTiming{
			ID:             w.id,
			DurationS:      w.durS,
			Pct:            pct,
			ConcurrentWith: concurrent,
		}
	}

	// Identify bottleneck: longest node
	var bottlenecks []Bottleneck
	if len(windows) > 0 {
		longest := windows[0]
		for _, w := range windows[1:] {
			if w.durS > longest.durS {
				longest = w
			}
		}
		bottlenecks = append(bottlenecks, Bottleneck{
			Node:      longest.id,
			Reason:    "longest duration",
			DurationS: longest.durS,
		})
	}

	return TimingResult{
		TotalS:      totalS,
		Nodes:       nodeTimes,
		Bottlenecks: bottlenecks,
	}
}
