package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/UserExistsError/conpty"
	"github.com/agent-infinite/agent-infinite/backend/internal/agent"
	"github.com/agent-infinite/agent-infinite/backend/internal/detector"
)

const RawOutputLimit = 2 * 1024 * 1024

var (
	ErrConPTYUnavailable = errors.New("ConPTY is unavailable on this Windows version")
	ErrSessionNotFound   = errors.New("terminal session not found")
)

type Session struct {
	id                string
	cpty              *conpty.ConPty
	cancel            context.CancelFunc
	close             sync.Once
	mu                sync.Mutex
	ring              *Ring
	subs              map[uint64]chan []byte
	nextSub           uint64
	startedAt         time.Time
	lastOutputAt      time.Time
	alive             bool
	dispatchActive    bool
	dispatchStartedAt time.Time
	lifecycleObserved bool
	lifecycleReady    bool
	detector          *detector.Machine
	screen            *detector.Screen
	status            detector.Status
	lifecycleStatus   detector.Status
	lifecycleUntil    time.Time
	cleanup           func()
	sequence          uint64
	integrationMode   string
	hookSessionID     string
	mcpConnected      bool
}

func startPowerShell(parent context.Context, workDir string) (*Session, error) {
	return startSession(parent, agent.LaunchSpec{CommandLine: "powershell.exe -NoLogo -NoProfile", WorkDir: workDir, Env: nil, Cleanup: func() {}})
}

func startSession(parent context.Context, spec agent.LaunchSpec) (*Session, error) {
	if !conpty.IsConPtyAvailable() {
		return nil, ErrConPTYUnavailable
	}
	options := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(120, 32),
		conpty.ConPtyWorkDir(spec.WorkDir),
	}
	if spec.Env != nil {
		options = append(options, conpty.ConPtyEnv(spec.Env))
	}
	cpty, err := conpty.Start(spec.CommandLine, options...)
	if err != nil {
		return nil, fmt.Errorf("start ConPTY: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	now := time.Now()
	session := &Session{
		id:              randomID(),
		cpty:            cpty,
		cancel:          cancel,
		ring:            NewRing(RawOutputLimit),
		subs:            make(map[uint64]chan []byte),
		startedAt:       now,
		lastOutputAt:    now,
		alive:           true,
		detector:        detector.New(),
		screen:          detector.NewScreen(120, 32),
		status:          detector.Starting,
		cleanup:         spec.Cleanup,
		integrationMode: spec.IntegrationMode,
		hookSessionID:   spec.HookSessionID,
	}
	go session.readOutput(ctx)
	go session.poll(ctx)
	return session, nil
}

func (s *Session) ID() string              { return s.id }
func (s *Session) IntegrationMode() string { return s.integrationMode }
func (s *Session) HookSessionID() string   { return s.hookSessionID }
func (s *Session) MCPConnected() bool      { s.mu.Lock(); defer s.mu.Unlock(); return s.mcpConnected }

func (s *Session) LifecycleReadiness() (observed, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lifecycleObserved, s.lifecycleReady
}

func (s *Session) SetMCPConnected(connected bool) {
	s.mu.Lock()
	s.mcpConnected = connected
	s.mu.Unlock()
}

func (s *Session) Write(data []byte) error {
	_, err := s.cpty.Write(data)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	s.screen.Resize(cols, rows)
	return s.cpty.Resize(cols, rows)
}

func (s *Session) Status() detector.Status { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *Session) Sequence() uint64        { s.mu.Lock(); defer s.mu.Unlock(); return s.sequence }
func (s *Session) CleanText() string       { return s.screen.Text() }
func (s *Session) SetDispatchActive(active bool) {
	s.mu.Lock()
	s.dispatchActive = active
	if active {
		s.dispatchStartedAt = time.Now()
		s.status = detector.Working
	} else {
		s.dispatchStartedAt = time.Time{}
	}
	s.mu.Unlock()
}

func (s *Session) SetLifecycleStatus(status detector.Status, hold time.Duration) {
	s.mu.Lock()
	s.lifecycleStatus = status
	s.lifecycleUntil = time.Now().Add(hold)
	s.status = status
	s.mu.Unlock()
}

// SetLifecycleReady records the provider's authoritative ability to accept a
// prompt. It persists between hook events, unlike SetLifecycleStatus which is
// intentionally a short-lived UI status hold.
func (s *Session) SetLifecycleReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycleObserved = true
	s.lifecycleReady = ready
	if !s.alive {
		return
	}
	// Ready=true is consumed by the polling detector so quiescence and visible
	// confirmation prompts still win. Busy can be applied immediately.
	if !ready {
		s.status = detector.Working
	}
}

func (s *Session) Attach() (initial []byte, live <-chan []byte, detach func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	channel := make(chan []byte, 64)
	s.subs[id] = channel
	return s.ring.Bytes(), channel, func() { s.detach(id) }
}

func (s *Session) Close() error {
	var err error
	s.close.Do(func() {
		s.cancel()
		s.mu.Lock()
		s.alive = false
		s.status = detector.Dead
		s.mu.Unlock()
		err = s.cpty.Close()
		if s.cleanup != nil {
			s.cleanup()
		}
		s.mu.Lock()
		for id, subscriber := range s.subs {
			delete(s.subs, id)
			close(subscriber)
		}
		s.mu.Unlock()
	})
	return err
}

func (s *Session) readOutput(ctx context.Context) {
	buffer := make([]byte, 16*1024)
	for {
		n, err := s.cpty.Read(buffer)
		if n > 0 {
			s.screen.Write(buffer[:n])
			s.mu.Lock()
			s.lastOutputAt = time.Now()
			s.mu.Unlock()
			s.broadcast(buffer[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				// The manager observes closure through the ended subscriber streams.
			}
			_ = s.Close()
			return
		}
	}
}

func (s *Session) poll(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			hasDescendants := HasDescendants(s.cpty.Pid())
			screen := s.screen.Text()
			s.mu.Lock()
			if now.Before(s.lifecycleUntil) {
				s.status = s.lifecycleStatus
				s.mu.Unlock()
				continue
			}
			signals := detector.Signals{Alive: s.alive, ActiveDescendants: hasDescendants, DispatchActive: s.dispatchActive, LifecycleObserved: s.lifecycleObserved, LifecycleReady: s.lifecycleReady, DispatchStartedAt: s.dispatchStartedAt, StartedAt: s.startedAt, LastOutputAt: s.lastOutputAt, Screen: screen}
			s.status = s.detector.Evaluate(now, signals)
			s.mu.Unlock()
		}
	}
}

func (s *Session) broadcast(data []byte) {
	chunk := append([]byte(nil), data...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring.Append(chunk)
	s.sequence++
	for id, subscriber := range s.subs {
		select {
		case subscriber <- chunk:
		default:
			delete(s.subs, id)
			close(subscriber)
		}
	}
}

func (s *Session) detach(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subscriber, ok := s.subs[id]; ok {
		delete(s.subs, id)
		close(subscriber)
	}
}

func randomID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(fmt.Errorf("generate session id: %w", err))
	}
	return hex.EncodeToString(data)
}
