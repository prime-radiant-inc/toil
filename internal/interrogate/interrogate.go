package interrogate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"primeradiant.com/toil/internal/runners"
	"primeradiant.com/toil/internal/state"
)

const ExpiryDuration = 30 * time.Minute

type Interrogation struct {
	ID              string
	RunID           string
	NodeID          string
	OrigSessionID   string
	ForkedSessionID string
	Workspace       string
	Runner          runners.Runner
	CreatedAt       time.Time
	LastActive      time.Time
	mu              sync.Mutex // serializes asks per interrogation
}

type CreateRequest struct {
	RunState  *state.RunState
	NodeID    string
	Question  string
	Runner    runners.Runner
	Workspace string
}

type CreateResult struct {
	ID              string `json:"id"`
	RunID           string `json:"run_id"`
	NodeID          string `json:"node_id"`
	OrigSessionID   string `json:"original_session_id"`
	ForkedSessionID string `json:"forked_session_id"`
	Response        string `json:"response"`
	CreatedAt       string `json:"created_at"`
}

type AskResult struct {
	Response string `json:"response"`
}

type ListEntry struct {
	ID         string `json:"id"`
	RunID      string `json:"run_id"`
	NodeID     string `json:"node_id"`
	CreatedAt  string `json:"created_at"`
	LastActive string `json:"last_active"`
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Interrogation
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Interrogation),
	}
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	var sessionID string
	req.RunState.WithNodes(func(nodes map[string]*state.NodeState) {
		if n, ok := nodes[req.NodeID]; ok {
			sessionID = n.SessionID
		}
	})
	if sessionID == "" {
		return CreateResult{}, fmt.Errorf("node %q has no session to interrogate", req.NodeID)
	}

	result, err := req.Runner.Run(ctx, runners.Request{
		Prompt:    req.Question,
		Workspace: req.Workspace,
		SessionID: sessionID,
		Resume:    true,
		Fork:      true,
	}, nil)
	if err != nil {
		return CreateResult{}, fmt.Errorf("fork run: %w", err)
	}

	// The runner MUST return a distinct forked session ID. If it's
	// empty or the same as the original, later Ask calls would resume
	// the original diagnostic-target session and mutate its state,
	// violating the "fork leaves original untouched" guarantee.
	forkedSessionID := result.SessionID
	if forkedSessionID == "" || forkedSessionID == sessionID {
		return CreateResult{}, fmt.Errorf("runner did not return a distinct forked session ID (got %q); refusing to create interrogation that would mutate the original session", forkedSessionID)
	}

	now := time.Now().UTC()
	id := "int-" + randomHex(8)

	entry := &Interrogation{
		ID:              id,
		RunID:           req.RunState.ID,
		NodeID:          req.NodeID,
		OrigSessionID:   sessionID,
		ForkedSessionID: forkedSessionID,
		Workspace:       req.Workspace,
		Runner:          req.Runner,
		CreatedAt:       now,
		LastActive:      now,
	}

	m.mu.Lock()
	m.sessions[id] = entry
	m.mu.Unlock()

	return CreateResult{
		ID:              id,
		RunID:           req.RunState.ID,
		NodeID:          req.NodeID,
		OrigSessionID:   sessionID,
		ForkedSessionID: forkedSessionID,
		Response:        result.Output,
		CreatedAt:       now.Format(time.RFC3339),
	}, nil
}

func (m *Manager) Ask(ctx context.Context, interrogationID string, question string) (AskResult, error) {
	m.mu.RLock()
	entry, ok := m.sessions[interrogationID]
	m.mu.RUnlock()
	if !ok {
		return AskResult{}, fmt.Errorf("interrogation %q not found", interrogationID)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	result, err := entry.Runner.Run(ctx, runners.Request{
		Prompt:    question,
		Workspace: entry.Workspace,
		SessionID: entry.ForkedSessionID,
		Resume:    true,
	}, nil)
	if err != nil {
		return AskResult{}, fmt.Errorf("ask run: %w", err)
	}

	entry.LastActive = time.Now().UTC()

	return AskResult{Response: result.Output}, nil
}

func (m *Manager) List() []ListEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make([]ListEntry, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		lastActive := s.LastActive.Format(time.RFC3339)
		s.mu.Unlock()
		entries = append(entries, ListEntry{
			ID:         s.ID,
			RunID:      s.RunID,
			NodeID:     s.NodeID,
			CreatedAt:  s.CreatedAt.Format(time.RFC3339),
			LastActive: lastActive,
		})
	}
	return entries
}

func (m *Manager) Sweep() {
	cutoff := time.Now().Add(-ExpiryDuration)

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, s := range m.sessions {
		s.mu.Lock()
		expired := s.LastActive.Before(cutoff)
		s.mu.Unlock()
		if expired {
			delete(m.sessions, id)
		}
	}
}

func (m *Manager) StartExpiry(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Sweep()
			}
		}
	}()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
