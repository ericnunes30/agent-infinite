package hookbridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrUnauthorized = errors.New("invalid or expired hook session")
	ErrMismatch     = errors.New("hook callback identity does not match its session")
	ErrDuplicate    = errors.New("duplicate hook callback")
	ErrOutOfOrder   = errors.New("hook callback is out of lifecycle order")
)

type Session struct {
	ID          string    `json:"sessionId"`
	NodeID      string    `json:"nodeId"`
	WorkspaceID string    `json:"workspaceId"`
	Provider    string    `json:"provider"`
	Mode        string    `json:"mode"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"createdAt"`
	LastEventAt time.Time `json:"lastEventAt,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Token       string    `json:"-"`
	seen        map[string]time.Time
}

type Callback struct {
	SessionID   string          `json:"sessionId"`
	NodeID      string          `json:"nodeId"`
	WorkspaceID string          `json:"workspaceId"`
	Provider    string          `json:"provider"`
	Event       string          `json:"event"`
	Raw         json.RawMessage `json:"raw"`
}

type Event struct {
	Session Session         `json:"session"`
	Name    string          `json:"name"`
	Raw     json.RawMessage `json:"raw"`
	At      time.Time       `json:"at"`
}

type Service struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	byToken  map[string]string
	onEvent  func(Event)
}

func New() *Service {
	return &Service{sessions: make(map[string]*Session), byToken: make(map[string]string), onEvent: func(Event) {}}
}

func (s *Service) SetObserver(observer func(Event)) {
	s.mu.Lock()
	s.onEvent = observer
	s.mu.Unlock()
}

func (s *Service) Register(nodeID, workspaceID, provider, mode string) Session {
	now := time.Now().UTC()
	session := &Session{
		ID: randomHex(16), NodeID: nodeID, WorkspaceID: workspaceID, Provider: provider,
		Mode: mode, State: "pending", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Token: randomToken(), seen: make(map[string]time.Time),
	}
	s.mu.Lock()
	s.sessions[session.ID] = session
	s.byToken[session.Token] = session.ID
	s.mu.Unlock()
	return publicCopy(session)
}

func (s *Service) Token(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if session := s.sessions[sessionID]; session != nil {
		return session.Token
	}
	return ""
}

func (s *Service) Close(sessionID string) {
	s.mu.Lock()
	if session := s.sessions[sessionID]; session != nil {
		delete(s.byToken, session.Token)
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
}

func (s *Service) MarkDegraded(sessionID string) {
	s.mu.Lock()
	if session := s.sessions[sessionID]; session != nil && session.State == "pending" {
		session.State = "degraded"
		session.Mode = "detector"
	}
	s.mu.Unlock()
}

func (s *Service) Session(sessionID string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session := s.sessions[sessionID]
	if session == nil {
		return Session{}, false
	}
	return publicCopy(session), true
}

func (s *Service) Handle(token string, callback Callback) (Event, error) {
	s.mu.Lock()
	sessionID, ok := s.byToken[token]
	session := s.sessions[sessionID]
	if !ok || session == nil || callback.SessionID != session.ID {
		s.mu.Unlock()
		return Event{}, ErrUnauthorized
	}
	if callback.NodeID != session.NodeID || callback.WorkspaceID != session.WorkspaceID || callback.Provider != session.Provider {
		s.mu.Unlock()
		return Event{}, ErrMismatch
	}
	name := normalizeEvent(callback.Event, callback.Raw)
	now := time.Now().UTC()
	if now.After(session.ExpiresAt) {
		delete(s.byToken, session.Token)
		delete(s.sessions, session.ID)
		s.mu.Unlock()
		return Event{}, ErrUnauthorized
	}
	if session.State == "pending" && name != "SessionStart" || session.State == "active" && name == "SessionStart" {
		s.mu.Unlock()
		return Event{}, ErrOutOfOrder
	}
	fingerprint := sha256.Sum256(append(append([]byte(name), 0), callback.Raw...))
	key := hex.EncodeToString(fingerprint[:])
	if previous, duplicate := session.seen[key]; duplicate && now.Sub(previous) < 2*time.Second {
		s.mu.Unlock()
		return Event{}, ErrDuplicate
	}
	session.seen[key] = now
	session.LastEventAt = now
	session.State = "active"
	session.Mode = "hooks"
	copy := publicCopy(session)
	observer := s.onEvent
	s.mu.Unlock()
	event := Event{Session: copy, Name: name, Raw: append(json.RawMessage(nil), callback.Raw...), At: now}
	observer(event)
	return event, nil
}

func (s *Service) Sessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, publicCopy(session))
	}
	return result
}

func normalizeEvent(explicit string, raw json.RawMessage) string {
	if explicit != "" {
		return explicit
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) == nil {
		for _, key := range []string{"hook_event_name", "hookEventName", "event", "type"} {
			if value, ok := payload[key].(string); ok && value != "" {
				return value
			}
		}
	}
	return "Unknown"
}

func publicCopy(session *Session) Session {
	copy := *session
	copy.Token = ""
	copy.seen = nil
	return copy
}

func randomHex(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}

func randomToken() string {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}
