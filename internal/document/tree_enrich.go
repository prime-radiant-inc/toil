package document

import "primeradiant.com/toil/internal/state"

// enrichRunNode walks a built tree and hydrates each RowChild with prompt,
// transcript, result, artifacts, and disclosure hint sourced from the
// per-execution event slice. Recurses into SubRunChild and ParallelChild.
// The loader may also implement EventLoader; only then are per-attempt
// prompts and transcripts populated. The function is safe to call with a
// loader that only implements Loader (rows simply receive no event-derived
// data).
func enrichRunNode(n *RunNode, loader Loader) {
	if n == nil {
		return
	}
	el, _ := loader.(EventLoader)
	var events []state.Event
	if el != nil {
		events = el.LoadEvents(n.RunID)
	}
	rs, err := loader.LoadRun(n.RunID)
	if err != nil {
		rs = nil
	}
	for i, c := range n.Children {
		switch row := c.(type) {
		case RowChild:
			ord := row.AttemptOrdinal
			slice := SliceExecutionEvents(events, row.NodeID, ord)

			// Prompt: prefer the LOCAL block of the per-execution node_prompt event.
			if rawPrompt := findPromptForExecution(events, row.NodeID, ord); rawPrompt != "" {
				local, _ := ExtractLocalPrompt(rawPrompt)
				if local != "" {
					row.Prompt = local
				} else {
					row.Prompt = rawPrompt
				}
			}

			// Per-attempt session/resume from this execution's node_started event.
			for _, ev := range slice {
				if ev.Type == eventNodeStarted && ev.NodeID == row.NodeID {
					if sid, ok := ev.Data["session_id"].(string); ok && sid != "" {
						row.SessionID = sid
					}
					if resume, ok := ev.Data["resume"].(bool); ok {
						row.IsResume = resume
					}
					break
				}
			}

			// Transcript scoped to this execution's events.
			transcript := BuildTranscript(row.NodeID, slice)
			row.Transcript = &transcript

			// Artifacts + disclosure hint from current NodeState (per-attempt
			// artifacts are not tracked separately in state).
			if rs != nil && rs.Nodes[row.NodeID] != nil {
				row.Artifacts = rowArtifacts(rs.Nodes[row.NodeID])
				row.DisclosureHint = disclosureHint(rs.Nodes[row.NodeID])
			}

			// Result: last `communicate` tool_call's message in this transcript,
			// falling back to NodeState.Message when none.
			if msg := lastCommunicateMessage(&transcript); msg != "" {
				row.Result = msg
			} else if rs != nil && rs.Nodes[row.NodeID] != nil {
				row.Result = rs.Nodes[row.NodeID].Message
			}

			n.Children[i] = row
		case SubRunChild:
			enrichRunNode(row.Run, loader)
		case ParallelChild:
			for _, r := range row.Runs {
				enrichRunNode(r, loader)
			}
		}
	}
}

// lastCommunicateMessage returns the `args.message` string from the last
// `communicate` tool call in the transcript's final attempt, or "" if absent.
func lastCommunicateMessage(t *Transcript) string {
	if t == nil || len(t.Attempts) == 0 {
		return ""
	}
	for ai := len(t.Attempts) - 1; ai >= 0; ai-- {
		att := t.Attempts[ai]
		for mi := len(att.Messages) - 1; mi >= 0; mi-- {
			m := att.Messages[mi]
			if m.Kind != kindToolCall || m.ToolCall == nil {
				continue
			}
			if m.ToolCall.ToolName != "communicate" {
				continue
			}
			if msg, _ := m.ToolCall.Args["message"].(string); msg != "" {
				return msg
			}
		}
	}
	return ""
}
