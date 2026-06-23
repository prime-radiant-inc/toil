package inspect

import (
	"sort"

	"primeradiant.com/toil/internal/state"
)

func init() {
	Register("transcript", func(rs *state.RunState) Processor { return NewTranscriptProcessor(rs) })
}

const argsPreviewMaxLen = 200

// TranscriptResult holds per-node transcripts grouped by attempt and round.
type TranscriptResult struct {
	Nodes []NodeTranscript `json:"nodes"`
}

// NodeTranscript holds the transcript for a single node.
type NodeTranscript struct {
	ID       string              `json:"id"`
	Attempts []TranscriptAttempt `json:"attempts"`
}

// TranscriptAttempt holds data for a single execution attempt of a node.
type TranscriptAttempt struct {
	Attempt   int               `json:"attempt"`
	SessionID string            `json:"session_id,omitempty"`
	Model     string            `json:"model,omitempty"`
	Rounds    []TranscriptRound `json:"rounds"`
	Decision  string            `json:"decision,omitempty"`
	Message   string            `json:"message,omitempty"`
}

// TranscriptRound holds the tool calls and timing for a single LLM round.
type TranscriptRound struct {
	Round     int             `json:"round"`
	DurationS float64         `json:"duration_s"`
	ToolCalls []ToolCallEntry `json:"tool_calls"`
}

// ToolCallEntry summarizes a single tool call.
type ToolCallEntry struct {
	Tool        string `json:"tool"`
	ArgsPreview string `json:"args_preview,omitempty"`
	ArgsSize    int    `json:"args_size,omitempty"`
}

// nodeTranscriptState tracks in-progress transcript data for one node.
type nodeTranscriptState struct {
	attempts       []TranscriptAttempt
	currentAttempt *TranscriptAttempt
	pendingCalls   []ToolCallEntry // tool calls in the current round (not yet finalized)
}

func (ns *nodeTranscriptState) ensureAttempt() {
	if ns.currentAttempt == nil {
		ns.currentAttempt = &TranscriptAttempt{
			Attempt: len(ns.attempts) + 1,
		}
	}
}

func (ns *nodeTranscriptState) finalizeRound(round int, durationS float64) {
	ns.ensureAttempt()
	ns.currentAttempt.Rounds = append(ns.currentAttempt.Rounds, TranscriptRound{
		Round:     round,
		DurationS: durationS,
		ToolCalls: ns.pendingCalls,
	})
	ns.pendingCalls = nil
}

func (ns *nodeTranscriptState) newAttempt() {
	if ns.currentAttempt != nil {
		ns.attempts = append(ns.attempts, *ns.currentAttempt)
	}
	ns.currentAttempt = &TranscriptAttempt{
		Attempt: len(ns.attempts) + 1,
	}
	ns.pendingCalls = nil
}

func (ns *nodeTranscriptState) allAttempts() []TranscriptAttempt {
	var all []TranscriptAttempt
	all = append(all, ns.attempts...)
	if ns.currentAttempt != nil {
		all = append(all, *ns.currentAttempt)
	}
	return all
}

type transcriptProcessor struct {
	nodes   map[string]*nodeTranscriptState
	order   []string // insertion order for node IDs
	changed bool
}

func NewTranscriptProcessor(rs *state.RunState) *transcriptProcessor {
	return &transcriptProcessor{
		nodes: make(map[string]*nodeTranscriptState),
	}
}

func (p *transcriptProcessor) getOrCreateNode(nodeID string) *nodeTranscriptState {
	ns, ok := p.nodes[nodeID]
	if !ok {
		ns = &nodeTranscriptState{}
		p.nodes[nodeID] = ns
		p.order = append(p.order, nodeID)
	}
	return ns
}

func (p *transcriptProcessor) ProcessEvent(event state.Event) {
	if event.Type == eventNodeStarted {
		ns := p.getOrCreateNode(event.NodeID)
		ns.newAttempt()
		p.changed = true
		return
	}

	inner, ok := ParseRunnerEvent(event)
	if !ok {
		return
	}

	ns := p.getOrCreateNode(inner.NodeID)

	switch {
	case inner.RoundTimings != nil:
		rt := inner.RoundTimings
		durationS := float64(rt.TotalRoundNs) / 1e9
		ns.finalizeRound(rt.Round, durationS)
		p.changed = true

	case inner.SessionStart != nil:
		ns.ensureAttempt()
		if inner.SessionStart.Model != "" {
			ns.currentAttempt.Model = inner.SessionStart.Model
		}
		if inner.SessionStart.SessionID != "" {
			ns.currentAttempt.SessionID = inner.SessionStart.SessionID
		}
		p.changed = true

	case inner.ToolCall != nil && inner.Communicate != nil:
		// communicate tool — record decision on the current attempt
		ns.ensureAttempt()
		ns.currentAttempt.Decision = inner.Communicate.Decision
		ns.currentAttempt.Message = inner.Communicate.Message
		p.changed = true

	case inner.ToolCall != nil:
		ns.ensureAttempt()
		argsJSON := inner.ToolCall.ArgumentsJSON
		preview := argsJSON
		if len(preview) > argsPreviewMaxLen {
			preview = preview[:argsPreviewMaxLen]
		}
		ns.pendingCalls = append(ns.pendingCalls, ToolCallEntry{
			Tool:        inner.ToolCall.Name,
			ArgsPreview: preview,
			ArgsSize:    len(argsJSON),
		})
		p.changed = true
	}
}

func (p *transcriptProcessor) Changed() bool {
	return p.changed
}

func (p *transcriptProcessor) Result() any {
	p.changed = false

	// Sort node IDs for deterministic output
	ids := make([]string, len(p.order))
	copy(ids, p.order)
	sort.Strings(ids)

	nodes := make([]NodeTranscript, 0, len(ids))
	for _, id := range ids {
		ns := p.nodes[id]
		attempts := ns.allAttempts()
		if attempts == nil {
			attempts = []TranscriptAttempt{}
		}
		nodes = append(nodes, NodeTranscript{
			ID:       id,
			Attempts: attempts,
		})
	}

	if nodes == nil {
		nodes = []NodeTranscript{}
	}

	return TranscriptResult{Nodes: nodes}
}
