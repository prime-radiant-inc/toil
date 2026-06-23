package api

import (
	"encoding/json"

	"github.com/getkin/kin-openapi/openapi3"
)

func BuildSpec() *openapi3.T {
	spec := &openapi3.T{
		OpenAPI: "3.0.0",
		Info: &openapi3.Info{
			Title:       "Toil Workflow Orchestrator API",
			Description: "Machine-readable API for agents to discover and interact with toil's workflow orchestration endpoints.",
			Version:     "1.0.0",
		},
		Paths: openapi3.NewPaths(),
	}

	addWorkflowPaths(spec)
	addRunPaths(spec)
	addApprovalPaths(spec)
	addInterrogationPaths(spec)
	addHealthPath(spec)

	return spec
}

func BuildSpecJSON() []byte {
	spec := BuildSpec()
	data, err := json.Marshal(spec)
	if err != nil {
		panic("openapi: marshal failed: " + err.Error())
	}
	return data
}

func addWorkflowPaths(spec *openapi3.T) {
	spec.Paths.Set("/workflows", &openapi3.PathItem{
		Get: newOp("listWorkflows", "List all available workflow definitions. Returns a JSON array of workflow ID strings."),
	})
	spec.Paths.Set("/workflows/{id}/graph", &openapi3.PathItem{
		Get: withParams(
			newOp("getWorkflowGraph", "Get the positioned graph layout for a workflow definition. Returns pre-computed x,y coordinates for rendering. Use this to visualize workflow structure before running it."),
			pathParam("id", "Workflow ID (e.g. 'deploy', 'learn')"),
		),
	})
	spec.Paths.Set("/workflows/{id}", &openapi3.PathItem{
		Get: &openapi3.Operation{
			OperationID: "getWorkflow",
			Description: "Get a single workflow definition as raw YAML text. Response Content-Type is text/plain, not JSON. Parse the YAML to inspect nodes, edges, inputs, and configuration.",
			Parameters:  openapi3.Parameters{pathParam("id", "Workflow ID (e.g. 'deploy', 'learn')")},
			Responses: openapi3.NewResponses(
				openapi3.WithStatus(200, &openapi3.ResponseRef{
					Value: &openapi3.Response{
						Description: ptr("Raw YAML workflow definition"),
						Content: openapi3.Content{
							"text/plain": {},
						},
					},
				}),
			),
		},
	})
}

func addRunPaths(spec *openapi3.T) {
	spec.Paths.Set("/runs", &openapi3.PathItem{
		Get: withParams(
			newOp("listRuns", "List runs. Without filters, returns a JSON array of run ID strings. With any filter, returns {\"runs\": [...]} with enriched runSummary objects sorted by started_at descending."),
			queryParam("callback_url", "Filter runs by callback URL prefix.", false),
			queryParam("workflow", "Filter runs by workflow ID (e.g. 'implement_spec').", false),
			queryParam(fieldStatus, "Filter runs by status (e.g. 'running', 'completed', 'failed').", false),
			queryParam("limit", "Max number of runs to return (applied after sorting).", false),
		),
		Post: withRequestBody(
			newOp("createRun", "Create and start a new workflow run. Returns {\"run_id\": \"<id>\"}. The run begins executing immediately."),
			jsonRequestBody("Run creation parameters", &openapi3.Schema{
				Type:     &openapi3.Types{schemaTypeObject},
				Required: []string{fieldWorkflowID},
				Properties: openapi3.Schemas{
					fieldWorkflowID: openapi3.NewSchemaRef("", &openapi3.Schema{
						Type:        &openapi3.Types{schemaTypeString},
						Description: "ID of the workflow to run (e.g. 'deploy', 'learn')",
					}),
					fieldInputs: openapi3.NewSchemaRef("", &openapi3.Schema{
						Type:        &openapi3.Types{schemaTypeObject},
						Description: "Key-value inputs passed to the workflow. Keys must match the workflow's declared inputs.",
					}),
					"env": openapi3.NewSchemaRef("", &openapi3.Schema{
						Type:        &openapi3.Types{schemaTypeObject},
						Description: "Environment variables passed to runner processes.",
						AdditionalProperties: openapi3.AdditionalProperties{
							Schema: openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{schemaTypeString}}),
						},
					}),
					"callback_url": openapi3.NewSchemaRef("", &openapi3.Schema{
						Type:        &openapi3.Types{schemaTypeString},
						Description: "Webhook URL called when the run reaches a terminal state.",
					}),
				},
			}),
		),
	})
	spec.Paths.Set("/runs/{id}", &openapi3.PathItem{
		Get: withParams(
			newOp("getRun", "Get full run state as JSON. Includes status, all node states, inputs, outputs, timing, and error information. This is the primary endpoint for checking run progress."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/cancel", &openapi3.PathItem{
		Post: withParams(
			newOp("cancelRun", "Cancel a running or paused run. No request body needed. Also cancels all child runs. Returns {\"status\": \"cancelled\"}. Fails if run is already in a terminal state."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/resume", &openapi3.PathItem{
		Post: withParams(
			newOp("resumeRun", "Resume a paused run. No request body needed. Returns {\"run_id\": \"<id>\"}. Use this after resolving an approval or to retry after a transient failure."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/retrigger", &openapi3.PathItem{
		Post: withParams(
			withRequestBody(
				newOp("retriggerNode", "Retrigger a failed node in an existing run. Resets the node and its downstream dependencies, then resumes execution. Also cascades resets to parent runs if applicable."),
				jsonRequestBody("Retrigger parameters", &openapi3.Schema{
					Type:     &openapi3.Types{schemaTypeObject},
					Required: []string{fieldNodeID},
					Properties: openapi3.Schemas{
						fieldNodeID: openapi3.NewSchemaRef("", &openapi3.Schema{
							Type:        &openapi3.Types{schemaTypeString},
							Description: "ID of the node to retrigger",
						}),
					},
				}),
			),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/events/stream", &openapi3.PathItem{
		Get: &openapi3.Operation{
			OperationID: "streamRunEvents",
			Description: "Stream run events in real-time via Server-Sent Events (SSE). Content-Type is text/event-stream. Events have named types matching the JSONL event types. Connection stays open until the run completes.",
			Parameters:  openapi3.Parameters{pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')")},
			Responses: openapi3.NewResponses(
				openapi3.WithStatus(200, &openapi3.ResponseRef{
					Value: &openapi3.Response{
						Description: ptr("SSE event stream"),
						Content: openapi3.Content{
							"text/event-stream": {},
						},
					},
				}),
			),
		},
	})
	spec.Paths.Set("/runs/{id}/events", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunEvents", "Get all events for a run. Response is JSONL (one JSON object per line, NOT a JSON array). Each line is a complete event object with type, timestamp, and event-specific data. Content-Type is application/json despite being JSONL."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/compound-graph", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunCompoundGraph", "Get the compound graph for a run, including all child/nested runs and their topology. Returns positioned graph data spanning the entire run tree. Use this to discover child runs without manually traversing parent_run fields."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/metrics", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunMetrics", "Get per-node and run-total metrics (duration, tokens, cost) for a single run. Returns {run_id, generated_at, run_total, nodes: {id: {status, own, rollup, ...}}}. Add ?follow=true to switch to an SSE stream of metric-update events (Content-Type text/event-stream) coalesced at 500ms."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/execution-group/metrics", &openapi3.PathItem{
		Get: withParams(
			newOp("getExecutionGroupMetrics", "Get metrics aggregated across the whole execution group rooted at this run. Walks the parent/child run tree and returns {root_run_id, group_total, runs: {run_id: {parent_run_id, run_total, nodes}}}."),
			pathParam("id", "Root run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/meta", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunMeta", "Get lightweight run metadata with a summary of each node's status and decision. Faster than getRun — omits full message/output data. Returns run_id, workflow_id, status, error, title, description, summary, timing, node map with {status, decision, error, data}, and inputs."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/graph", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunGraph", "Get the positioned graph layout for a specific run. Like getWorkflowGraph but reflects actual run state (node statuses, ForEach expansion, compound nodes). Use for rendering run progress visualizations."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/document", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunDocument", "Get the document tree for a run's execution group. Returns {root: RunNode} where each RunNode has metadata (run_id, workflow_id, decision, compact flag, summary, duration_ms) and a chronological 'children' array. Each child is one of: a row (kind: \"row\", representing one node execution), a subrun (kind: \"subrun\", with a nested run RunNode), or a parallel (kind: \"parallel\", with a 'runs' array of RunNodes from one ForEach iteration). Pass any run id in the execution group — the endpoint climbs to the root automatically. External consumers must adapt to this shape; the legacy items-list was removed alongside the dashboard refactor."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/document/row/{nodeId}", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunDocumentRow", "Get the disclosure detail for a single node row: inputs (run-level inputs), outputs (node data and decision), and transcript metadata (rounds, tokens, model). Used by the run document view to lazy-load row detail on expand."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
			pathParam("nodeId", "Node ID within the run"),
		),
	})
	spec.Paths.Set("/runs/{id}/session/{sid}", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunSessionDetail", "Get the per-node-attempt sequence for a given LLM session ID within a run. Returns {session_id, parts: [{node, decision, message, ts}]}. Useful for viewing the full session history when a node was resumed across multiple attempts."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
			pathParam("sid", "Session ID to look up"),
		),
	})
	spec.Paths.Set("/runs/{id}/inspect/{aspect}", &openapi3.PathItem{
		Get: withParams(
			newOp("inspectRun", "Inspect a run with a named aspect processor. Returns aspect-specific analysis computed from run state and events. Default aspect is 'overview'."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
			pathParam("aspect", "Inspect aspect (e.g. 'overview', 'flow', 'timing', 'tokens', 'errors')"),
		),
	})
	spec.Paths.Set("/runs/{id}/nodes/{nodeId}/inspect/{aspect}", &openapi3.PathItem{
		Get: withParams(
			newOp("inspectRunNode", "Inspect a specific node within a run with a named aspect processor. Returns node-specific analysis."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
			pathParam("nodeId", "Node ID within the run"),
			pathParam("aspect", "Inspect aspect (e.g. 'overview', 'flow', 'timing', 'tokens', 'errors')"),
		),
	})
	spec.Paths.Set("/runs/{id}/interviews", &openapi3.PathItem{
		Get: withParams(
			newOp("listRunInterviews", "List interview nodes for a run. Returns nodes that are candidates for or have completed post-run interviews."),
			pathParam("id", "Run ID (e.g. 'canyon-nebula-pine')"),
		),
	})
	spec.Paths.Set("/runs/{id}/interviews/{nodeId}", &openapi3.PathItem{
		Get: withParams(
			newOp("getRunInterview", "Get details for a specific interview node within a run."),
			pathParam("id", "Run ID"),
			pathParam("nodeId", "Node ID within the run"),
		),
	})
}

func addApprovalPaths(spec *openapi3.T) {
	spec.Paths.Set("/approvals", &openapi3.PathItem{
		Get: newOp("listApprovals", "List all pending approvals across all runs. Returns an array of approval objects with run_id, node_id, and approval metadata."),
	})
	spec.Paths.Set("/approvals/{id}/resolve", &openapi3.PathItem{
		Post: withParams(
			withRequestBody(
				newOp("resolveApproval", "Resolve a pending approval. The run will resume automatically after resolution."),
				jsonRequestBody("Approval resolution", &openapi3.Schema{
					Type:     &openapi3.Types{schemaTypeObject},
					Required: []string{fieldDecision, fieldMessage},
					Properties: openapi3.Schemas{
						fieldDecision: openapi3.NewSchemaRef("", &openapi3.Schema{
							Type:        &openapi3.Types{schemaTypeString},
							Description: "Approval decision (e.g. 'approved', 'rejected')",
						}),
						fieldMessage: openapi3.NewSchemaRef("", &openapi3.Schema{
							Type:        &openapi3.Types{schemaTypeString},
							Description: "Human-readable explanation of the decision",
						}),
						"comment": openapi3.NewSchemaRef("", &openapi3.Schema{
							Type:        &openapi3.Types{schemaTypeString},
							Description: "Optional additional comment",
						}),
					},
				}),
			),
			pathParam("id", "Approval ID"),
		),
	})
}

func addInterrogationPaths(spec *openapi3.T) {
	spec.Paths.Set("/interrogations", &openapi3.PathItem{
		Post: newOp("createInterrogation", "Fork a runner session and ask a diagnostic question."),
		Get:  newOp("listInterrogations", "List active interrogation sessions."),
	})
	spec.Paths.Set("/interrogations/{id}/ask", &openapi3.PathItem{
		Post: withParams(
			newOp("askInterrogation", "Ask a follow-up question in an existing interrogation."),
			pathParam("id", "Interrogation ID"),
		),
	})
}

func addHealthPath(spec *openapi3.T) {
	spec.Paths.Set("/health", &openapi3.PathItem{
		Get: newOp("getHealth", "Health check. Returns {status, uptime_seconds, active_runs, total_runs}. Use this to verify the server is reachable and check current load."),
	})
}

func newOp(operationID, description string) *openapi3.Operation {
	return &openapi3.Operation{
		OperationID: operationID,
		Description: description,
		Responses:   openapi3.NewResponses(),
	}
}

func pathParam(name, description string) *openapi3.ParameterRef {
	return &openapi3.ParameterRef{
		Value: &openapi3.Parameter{
			Name:        name,
			In:          "path",
			Required:    true,
			Description: description,
			Schema: openapi3.NewSchemaRef("", &openapi3.Schema{
				Type: &openapi3.Types{schemaTypeString},
			}),
		},
	}
}

func queryParam(name, description string, required bool) *openapi3.ParameterRef {
	return &openapi3.ParameterRef{
		Value: &openapi3.Parameter{
			Name:        name,
			In:          "query",
			Required:    required,
			Description: description,
			Schema: openapi3.NewSchemaRef("", &openapi3.Schema{
				Type: &openapi3.Types{schemaTypeString},
			}),
		},
	}
}

func withParams(op *openapi3.Operation, params ...*openapi3.ParameterRef) *openapi3.Operation {
	op.Parameters = params
	return op
}

func withRequestBody(op *openapi3.Operation, body *openapi3.RequestBodyRef) *openapi3.Operation {
	op.RequestBody = body
	return op
}

func ptr(s string) *string { return &s }

func jsonRequestBody(description string, schema *openapi3.Schema) *openapi3.RequestBodyRef {
	return &openapi3.RequestBodyRef{
		Value: &openapi3.RequestBody{
			Description: description,
			Required:    true,
			Content: openapi3.Content{
				contentTypeJSON: {
					Schema: openapi3.NewSchemaRef("", schema),
				},
			},
		},
	}
}
