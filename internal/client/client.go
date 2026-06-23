package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type runRequest struct {
	WorkflowID string            `json:"workflow_id"`
	Inputs     map[string]any    `json:"inputs"`
	Env        map[string]string `json:"env,omitempty"`
}

type runResponse struct {
	RunID string `json:"run_id"`
}

func NewFromEnv() *Client {
	base := os.Getenv("TOIL_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (client *Client) CreateRun(ctx context.Context, workflowID string, inputs map[string]any, env map[string]string) (string, error) {
	payload := runRequest{WorkflowID: workflowID, Inputs: inputs, Env: env}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	resp, err := client.do(ctx, http.MethodPost, "/runs", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", readError(resp)
	}

	var result runResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.RunID, nil
}

func (client *Client) ResumeRun(ctx context.Context, runID string) error {
	resp, err := client.do(ctx, http.MethodPost, "/runs/"+runID+"/resume", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}
	return nil
}

func (client *Client) CancelRun(ctx context.Context, runID string) error {
	resp, err := client.do(ctx, http.MethodPost, "/runs/"+runID+"/cancel", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}
	return nil
}

func (client *Client) ListRuns(ctx context.Context) ([]string, error) {
	resp, err := client.do(ctx, http.MethodGet, "/runs", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readError(resp)
	}
	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (client *Client) ListRunsFiltered(ctx context.Context, workflow string, status string, limit int) ([]byte, error) {
	// Build via url.Values so workflow/status values containing &, +,
	// spaces, or other reserved characters get escaped correctly.
	q := url.Values{}
	if workflow != "" {
		q.Set("workflow", workflow)
	}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/runs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return client.getBytes(ctx, path)
}

func (client *Client) RunState(ctx context.Context, runID string) ([]byte, error) {
	return client.getBytes(ctx, "/runs/"+runID)
}

func (client *Client) RunEvents(ctx context.Context, runID string) ([]byte, error) {
	return client.getBytes(ctx, "/runs/"+runID+"/events")
}

func (client *Client) RunGraph(ctx context.Context, runID string) ([]byte, error) {
	return client.getBytes(ctx, "/runs/"+runID+"/graph")
}

func (client *Client) Inspect(ctx context.Context, runID string, aspect string) ([]byte, error) {
	path := "/runs/" + runID + "/inspect"
	if aspect != "" {
		path += "/" + aspect
	}
	return client.getBytes(ctx, path)
}

// InspectFollow opens an SSE stream for the given run/aspect and calls handler
// for each SSE data frame. It blocks until the server closes the stream or
// the context is cancelled.
func (client *Client) InspectFollow(ctx context.Context, runID string, aspect string, handler func(data []byte)) error {
	path := "/runs/" + runID + "/inspect"
	if aspect != "" {
		path += "/" + aspect
	}
	path += "?follow=true"

	resp, err := client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}

	return streamSSEDataLines(resp.Body, handler)
}

// streamSSEDataLines reads an SSE stream line-by-line and invokes
// handler for each `data: ` frame. Uses bufio.Reader (not Scanner)
// so large payloads — ForEach items[], transcript aspects, tree
// dumps — don't hit the 64 KiB Scanner.Scan token limit and silently
// stop the stream.
func streamSSEDataLines(body io.Reader, handler func([]byte)) error {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
				handler([]byte(strings.TrimPrefix(line, "data: ")))
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (client *Client) InspectNode(ctx context.Context, runID string, nodeID string, aspect string) ([]byte, error) {
	return client.InspectNodeAttempt(ctx, runID, nodeID, aspect, "")
}

// InspectNodeAttempt is InspectNode with an optional attempt query
// parameter. Pass attempt="" to get all attempts (default).
func (client *Client) InspectNodeAttempt(ctx context.Context, runID string, nodeID string, aspect string, attempt string) ([]byte, error) {
	path := "/runs/" + runID + "/nodes/" + nodeID + "/inspect"
	if aspect != "" {
		path += "/" + aspect
	}
	if attempt != "" {
		path += "?attempt=" + url.QueryEscape(attempt)
	}
	return client.getBytes(ctx, path)
}

// InspectNodeFollow opens an SSE stream scoped to a specific node and
// calls handler for each SSE data frame. Same semantics as
// InspectFollow but for node-scoped routes.
func (client *Client) InspectNodeFollow(ctx context.Context, runID string, nodeID string, aspect string, handler func(data []byte)) error {
	path := "/runs/" + runID + "/nodes/" + nodeID + "/inspect"
	if aspect != "" {
		path += "/" + aspect
	}
	path += "?follow=true"

	resp, err := client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}

	return streamSSEDataLines(resp.Body, handler)
}

func (client *Client) ListApprovals(ctx context.Context) ([]byte, error) {
	return client.getBytes(ctx, "/approvals")
}

func (client *Client) ResolveApproval(ctx context.Context, approvalID string, decision string, message string, comment string) error {
	payload := map[string]string{"decision": decision, "message": message}
	if comment != "" {
		payload["comment"] = comment
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := client.do(ctx, http.MethodPost, "/approvals/"+approvalID+"/resolve", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}
	return nil
}

func (client *Client) InterrogationCreate(ctx context.Context, runID string, nodeID string, question string) ([]byte, error) {
	body := map[string]string{
		"run_id":   runID,
		"node_id":  nodeID,
		"question": question,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := client.do(ctx, http.MethodPost, "/interrogations", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readError(resp)
	}
	return io.ReadAll(resp.Body)
}

func (client *Client) InterrogationAsk(ctx context.Context, interrogationID string, question string) ([]byte, error) {
	body := map[string]string{"question": question}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := client.do(ctx, http.MethodPost, "/interrogations/"+interrogationID+"/ask", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readError(resp)
	}
	return io.ReadAll(resp.Body)
}

func (client *Client) InterrogationList(ctx context.Context) ([]byte, error) {
	return client.getBytes(ctx, "/interrogations")
}

func (client *Client) ListWorkflows(ctx context.Context) ([]string, error) {
	resp, err := client.do(ctx, http.MethodGet, "/workflows", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readError(resp)
	}
	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (client *Client) WorkflowShow(ctx context.Context, workflowID string) ([]byte, error) {
	return client.getBytes(ctx, "/workflows/"+workflowID)
}

func (client *Client) WorkflowGraph(ctx context.Context, workflowID string) ([]byte, error) {
	return client.getBytes(ctx, "/workflows/"+workflowID+"/graph")
}

func (client *Client) getBytes(ctx context.Context, path string) ([]byte, error) {
	resp, err := client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readError(resp)
	}
	return io.ReadAll(resp.Body)
}

func (client *Client) do(ctx context.Context, method string, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, client.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Toil server at %s: %w", client.BaseURL, err)
	}
	return resp, nil
}

func readError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("server error (%s): %s", resp.Status, message)
}
