package terminal

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/agent"
	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
)

type Manager struct {
	ctx            context.Context
	mu             sync.RWMutex
	sessions       map[string]*Session
	nodes          map[string]string
	statusObserver func(string, detector.Status)
	outputObserver func(string, string, uint64)
	lifecycle      func(string, string, string)
}

type Runtime struct {
	NodeID          string          `json:"nodeId"`
	SessionID       string          `json:"sessionId"`
	Status          detector.Status `json:"status"`
	IntegrationMode string          `json:"integrationMode"`
	HookSessionID   string          `json:"hookSessionId,omitempty"`
}

func (m *Manager) SetStatusObserver(observer func(string, detector.Status)) {
	m.mu.Lock()
	m.statusObserver = observer
	m.mu.Unlock()
}

func (m *Manager) SetOutputObserver(observer func(string, string, uint64)) {
	m.mu.Lock()
	m.outputObserver = observer
	m.mu.Unlock()
}

func (m *Manager) SetLifecycleObserver(observer func(string, string, string)) {
	m.mu.Lock()
	m.lifecycle = observer
	m.mu.Unlock()
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{ctx: ctx, sessions: make(map[string]*Session), nodes: make(map[string]string)}
}

func (m *Manager) StartNode(nodeID string, spec agent.LaunchSpec) (*Session, error) {
	m.mu.Lock()
	if sessionID, exists := m.nodes[nodeID]; exists {
		session := m.sessions[sessionID]
		m.mu.Unlock()
		return session, nil
	}
	m.mu.Unlock()
	session, err := startSession(m.ctx, spec)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[session.ID()] = session
	m.nodes[nodeID] = session.ID()
	observer := m.statusObserver
	outputObserver := m.outputObserver
	lifecycle := m.lifecycle
	m.mu.Unlock()
	if lifecycle != nil {
		lifecycle("terminal.started", nodeID, session.ID())
	}
	if observer != nil || outputObserver != nil || lifecycle != nil {
		go m.watchNode(nodeID, session)
	}
	return session, nil
}

func (m *Manager) watchNode(nodeID string, session *Session) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := detector.Status("")
	var lastSequence uint64
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			status := session.Status()
			sequence := session.Sequence()
			m.mu.RLock()
			statusObserver, outputObserver, lifecycle := m.statusObserver, m.outputObserver, m.lifecycle
			m.mu.RUnlock()
			if status != last && statusObserver != nil {
				statusObserver(nodeID, status)
				last = status
			}
			if sequence != lastSequence && outputObserver != nil {
				outputObserver(nodeID, preview(session.CleanText(), 320), sequence)
				lastSequence = sequence
			}
			if status == detector.Dead {
				if lifecycle != nil {
					lifecycle("terminal.exited", nodeID, session.ID())
				}
				return
			}
		}
	}
}

func preview(text string, limit int) string {
	text = strings.TrimRight(text, " \t\r\n\x00")
	characters := []rune(text)
	if len(characters) <= limit {
		return string(characters)
	}
	return string(characters[len(characters)-limit:])
}

func (m *Manager) StopNode(nodeID string) error {
	m.mu.Lock()
	sessionID, ok := m.nodes[nodeID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(m.nodes, nodeID)
	session := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	return session.Close()
}

func (m *Manager) StartPowerShell(workDir string) (*Session, error) {
	session, err := startPowerShell(m.ctx, workDir)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[session.ID()] = session
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return session, nil
}

func (m *Manager) GetByNode(nodeID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessionID, ok := m.nodes[nodeID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return session, nil
}

func (m *Manager) Runtimes() []Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Runtime, 0, len(m.nodes))
	for nodeID, sessionID := range m.nodes {
		if session, ok := m.sessions[sessionID]; ok {
			result = append(result, Runtime{NodeID: nodeID, SessionID: sessionID, Status: session.Status(), IntegrationMode: session.IntegrationMode(), HookSessionID: session.HookSessionID()})
		}
	}
	return result
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*Session)
	m.nodes = make(map[string]string)
	m.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
}
