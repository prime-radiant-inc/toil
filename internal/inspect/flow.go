package inspect

import (
	"sort"
	"time"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("flow", func(rs *state.RunState) Processor { return NewFlowProcessor(rs) })
}

type FlowResult struct {
	Events      []FlowEvent      `json:"events"`
	Annotations []FlowAnnotation `json:"annotations"`
}

type FlowEvent struct {
	Type      string  `json:"type"` // started, decision, edge, completed, failed, steering
	Node      string  `json:"node,omitempty"`
	From      string  `json:"from,omitempty"`
	To        string  `json:"to,omitempty"`
	Decision  string  `json:"decision,omitempty"`
	Message   string  `json:"message,omitempty"`
	Prompt    string  `json:"prompt,omitempty"`
	ChildRun  string  `json:"child_run,omitempty"`
	DurationS float64 `json:"duration_s,omitempty"`
	Ts        string  `json:"ts"`
}

type FlowAnnotation struct {
	Type       string   `json:"type"` // loop_detected, steering, concurrent
	Nodes      []string `json:"nodes,omitempty"`
	Node       string   `json:"node,omitempty"` // for steering (single node)
	Count      int      `json:"count,omitempty"`
	ResolvedBy string   `json:"resolved_by,omitempty"` // for loops: what decision broke the cycle
	WallS      float64  `json:"wall_s,omitempty"`      // for concurrent: wall clock time
}

// nodeWindow tracks a node's start/complete timestamps for concurrency detection.
type nodeWindow struct {
	start time.Time
	end   time.Time
}

// edgeTransition records a from→to edge transition for loop detection.
type edgeTransition struct {
	from     string
	to       string
	decision string // the decision that caused this edge
}

type flowProcessor struct {
	rs      *state.RunState
	events  []FlowEvent
	changed bool

	// Track the last node that made a decision, for edge "from" inference.
	lastDecidingNode string
	lastDecision     string

	// Track consecutive edge transitions for loop detection.
	edges []edgeTransition

	// Track per-node steering counts.
	steeringCounts map[string]int

	// Track node start/complete times for concurrency detection.
	nodeWindows map[string]*nodeWindow
}

func NewFlowProcessor(rs *state.RunState) *flowProcessor {
	return &flowProcessor{
		rs:             rs,
		steeringCounts: make(map[string]int),
		nodeWindows:    make(map[string]*nodeWindow),
	}
}

func (p *flowProcessor) ProcessEvent(event state.Event) {
	switch event.Type {
	case eventNodeStarted:
		p.events = append(p.events, FlowEvent{
			Type: "started",
			Node: event.NodeID,
			Ts:   formatTS(event.Timestamp),
		})
		p.nodeWindows[event.NodeID] = &nodeWindow{start: event.Timestamp}
		p.changed = true

	case eventNodeCompleted:
		var durS float64
		if event.DurationMs != nil {
			durS = float64(*event.DurationMs) / 1000.0
		}
		p.events = append(p.events, FlowEvent{
			Type:      flowTypeCompleted,
			Node:      event.NodeID,
			DurationS: durS,
			Ts:        formatTS(event.Timestamp),
		})
		if w, ok := p.nodeWindows[event.NodeID]; ok {
			w.end = event.Timestamp
		}
		p.changed = true

	case eventNodeFailed:
		p.events = append(p.events, FlowEvent{
			Type: "failed",
			Node: event.NodeID,
			Ts:   formatTS(event.Timestamp),
		})
		if w, ok := p.nodeWindows[event.NodeID]; ok {
			w.end = event.Timestamp
		}
		p.changed = true

	case "node_failed_handled":
		p.events = append(p.events, FlowEvent{
			Type: "failed_handled",
			Node: event.NodeID,
			Ts:   formatTS(event.Timestamp),
		})
		if w, ok := p.nodeWindows[event.NodeID]; ok {
			w.end = event.Timestamp
		}
		p.changed = true

	case "node_skipped":
		p.events = append(p.events, FlowEvent{
			Type: "skipped",
			Node: event.NodeID,
			Ts:   formatTS(event.Timestamp),
		})
		if w, ok := p.nodeWindows[event.NodeID]; ok {
			w.end = event.Timestamp
		}
		p.changed = true

	case eventNodeEdgePrompt:
		from := p.lastDecidingNode
		p.events = append(p.events, FlowEvent{
			Type:   "edge",
			From:   from,
			To:     event.NodeID,
			Prompt: event.Text,
			Ts:     formatTS(event.Timestamp),
		})
		p.edges = append(p.edges, edgeTransition{
			from:     from,
			to:       event.NodeID,
			decision: p.lastDecision,
		})
		p.changed = true

	case eventNodeOutput:
		inner, ok := ParseRunnerEvent(event)
		if !ok {
			return
		}

		if inner.Communicate != nil {
			p.events = append(p.events, FlowEvent{
				Type:     flowTypeDecision,
				Node:     inner.NodeID,
				Decision: inner.Communicate.Decision,
				Message:  inner.Communicate.Message,
				Ts:       formatTS(event.Timestamp),
			})
			p.lastDecidingNode = inner.NodeID
			p.lastDecision = inner.Communicate.Decision
			p.changed = true
		}

		if inner.Kind == kindSteeringInjected {
			p.events = append(p.events, FlowEvent{
				Type: flowTypeSteering,
				Node: inner.NodeID,
				Ts:   formatTS(event.Timestamp),
			})
			p.steeringCounts[inner.NodeID]++
			p.changed = true
		}
	}
}

func (p *flowProcessor) Changed() bool {
	return p.changed
}

func (p *flowProcessor) Result() any {
	p.changed = false

	events := p.events
	if events == nil {
		events = []FlowEvent{}
	}

	annotations := p.computeAnnotations()
	if annotations == nil {
		annotations = []FlowAnnotation{}
	}

	return FlowResult{
		Events:      events,
		Annotations: annotations,
	}
}

func (p *flowProcessor) computeAnnotations() []FlowAnnotation {
	loops := p.detectLoops()
	steering := p.detectSteering()
	concurrent := p.detectConcurrent()

	annotations := make([]FlowAnnotation, 0, len(loops)+len(steering)+len(concurrent))
	annotations = append(annotations, loops...)
	annotations = append(annotations, steering...)
	annotations = append(annotations, concurrent...)

	return annotations
}

func (p *flowProcessor) detectLoops() []FlowAnnotation {
	var annotations []FlowAnnotation

	if len(p.edges) < 3 {
		return nil
	}

	// Walk through edges looking for consecutive repetitions of the same (from, to) pair.
	// A loop is a cycle, so we look for alternating A→B, B→A sequences (or any repeating pair).
	// We track runs of the same directed pair.
	type pairKey struct{ from, to string }

	i := 0
	for i < len(p.edges) {
		// Try to find a repeating pair starting at position i.
		pair := pairKey{p.edges[i].from, p.edges[i].to}
		count := 1
		j := i + 1

		// Count consecutive occurrences of this same pair
		for j < len(p.edges) && p.edges[j].from == pair.from && p.edges[j].to == pair.to {
			count++
			j++
		}

		if count >= 3 {
			resolvedBy := ""
			if j < len(p.edges) {
				resolvedBy = p.edges[j].decision
			}
			annotations = append(annotations, FlowAnnotation{
				Type:       flowTypeLoopDetected,
				Nodes:      []string{pair.from, pair.to},
				Count:      count,
				ResolvedBy: resolvedBy,
			})
			i = j
			continue
		}

		// Also detect cycles: alternating A→B, B→A pattern
		// Look for the pattern where edges form a cycle between two nodes
		if i+1 < len(p.edges) {
			first := p.edges[i]
			second := pairKey{p.edges[i+1].from, p.edges[i+1].to}
			// If first is A→B and second is B→A, that's a cycle pair
			if first.from == second.to && first.to == second.from {
				cycleCount := 0
				k := i
				for k+1 < len(p.edges) {
					if p.edges[k].from == first.from && p.edges[k].to == first.to &&
						p.edges[k+1].from == second.from && p.edges[k+1].to == second.to {
						cycleCount++
						k += 2
					} else {
						break
					}
				}
				if cycleCount >= 3 {
					resolvedBy := ""
					// Find the decision that broke the cycle. After the matched
					// pairs, there may be a partial cycle start (edges[k] matches
					// the first direction). The actual breaker is the next edge
					// that goes somewhere different.
					resolveIdx := k
					if resolveIdx < len(p.edges) &&
						p.edges[resolveIdx].from == first.from && p.edges[resolveIdx].to == first.to {
						resolveIdx++
					}
					if resolveIdx < len(p.edges) {
						resolvedBy = p.edges[resolveIdx].decision
					}
					annotations = append(annotations, FlowAnnotation{
						Type:       flowTypeLoopDetected,
						Nodes:      []string{first.from, first.to},
						Count:      cycleCount,
						ResolvedBy: resolvedBy,
					})
					i = resolveIdx
					if i < len(p.edges) {
						i++
					}
					continue
				}
			}
		}
		i++
	}

	return annotations
}

func (p *flowProcessor) detectSteering() []FlowAnnotation {
	var annotations []FlowAnnotation

	// Sort node names for deterministic output
	nodes := make([]string, 0, len(p.steeringCounts))
	for node := range p.steeringCounts {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)

	for _, node := range nodes {
		count := p.steeringCounts[node]
		if count > 0 {
			annotations = append(annotations, FlowAnnotation{
				Type:  flowTypeSteering,
				Node:  node,
				Count: count,
			})
		}
	}

	return annotations
}

func (p *flowProcessor) detectConcurrent() []FlowAnnotation {
	var annotations []FlowAnnotation

	// Collect completed windows (nodes that have both start and end times)
	type completedWindow struct {
		id    string
		start time.Time
		end   time.Time
	}
	var windows []completedWindow
	for id, w := range p.nodeWindows {
		if !w.start.IsZero() && !w.end.IsZero() {
			windows = append(windows, completedWindow{id: id, start: w.start, end: w.end})
		}
	}

	if len(windows) < 2 {
		return nil
	}

	// Sort by start time for deterministic grouping
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].start.Equal(windows[j].start) {
			return windows[i].id < windows[j].id
		}
		return windows[i].start.Before(windows[j].start)
	})

	// Find groups of overlapping nodes using a sweep approach.
	// Track which nodes have been assigned to a group.
	assigned := make(map[string]bool)

	for i := 0; i < len(windows); i++ {
		if assigned[windows[i].id] {
			continue
		}

		group := []completedWindow{windows[i]}
		earliestStart := windows[i].start
		latestEnd := windows[i].end

		for j := i + 1; j < len(windows); j++ {
			if assigned[windows[j].id] {
				continue
			}
			// Check if windows[j] overlaps with the current group window
			if windows[j].start.Before(latestEnd) {
				group = append(group, windows[j])
				if windows[j].end.After(latestEnd) {
					latestEnd = windows[j].end
				}
			}
		}

		if len(group) >= 2 {
			nodeIDs := make([]string, len(group))
			for k, g := range group {
				nodeIDs[k] = g.id
				assigned[g.id] = true
			}
			sort.Strings(nodeIDs)
			wallS := latestEnd.Sub(earliestStart).Seconds()
			annotations = append(annotations, FlowAnnotation{
				Type:  "concurrent",
				Nodes: nodeIDs,
				WallS: wallS,
			})
		}
	}

	return annotations
}

func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
