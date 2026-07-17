package detector

import (
	"regexp"
	"strings"
	"time"
)

type Status string

const (
	Starting Status = "Starting"
	Idle     Status = "Idle"
	Working  Status = "Working"
	Blocked  Status = "Blocked"
	Done     Status = "Done"
	Dead     Status = "Dead"
)

const (
	InitialGrace    = 1500 * time.Millisecond
	Quiescence      = 400 * time.Millisecond
	DoneHold        = time.Second
	CompletionGrace = 2 * time.Second
)

var (
	promptPattern  = regexp.MustCompile(`^\s*(?:[❯›](?:\s+[^\r\n]*)?|>)\s*$`)
	footerPattern  = regexp.MustCompile(`(?i)^(?:gpt|claude|gemini)[-\w.]*\b.*[·•].*$`)
	blockedPattern = regexp.MustCompile(`(?i)(Do you want|Shall I proceed|\(y/n\)|Press Enter|Allow this|Approve)`)
)

type Signals struct {
	Alive             bool
	ActiveDescendants bool
	DispatchActive    bool
	DispatchStartedAt time.Time
	StartedAt         time.Time
	LastOutputAt      time.Time
	Screen            string
}

type Machine struct {
	status               Status
	doneAt               time.Time
	dispatchObservedBusy bool
}

func New() *Machine               { return &Machine{status: Starting} }
func (m *Machine) Status() Status { return m.status }

func (m *Machine) Evaluate(now time.Time, signals Signals) Status {
	if !signals.Alive {
		m.status = Dead
		return m.status
	}
	if now.Sub(signals.StartedAt) < InitialGrace {
		m.status = Starting
		return m.status
	}
	if blockedPattern.MatchString(signals.Screen) {
		m.status = Blocked
		return m.status
	}
	promptReady := screenPromptReady(signals.Screen)
	if !signals.DispatchActive {
		m.dispatchObservedBusy = false
	} else if !promptReady {
		m.dispatchObservedBusy = true
	}
	if now.Sub(signals.LastOutputAt) < Quiescence {
		m.status = Working
		return m.status
	}
	if signals.DispatchActive && promptReady {
		completionReady := m.dispatchObservedBusy || (!signals.DispatchStartedAt.IsZero() && now.Sub(signals.DispatchStartedAt) >= CompletionGrace && signals.LastOutputAt.After(signals.DispatchStartedAt))
		if !completionReady {
			m.status = Working
			return m.status
		}
		if m.status != Done {
			m.status, m.doneAt = Done, now
		} else if now.Sub(m.doneAt) >= DoneHold {
			m.status = Idle
		}
		return m.status
	}
	if signals.DispatchActive {
		m.status = Working
	} else if signals.ActiveDescendants && !promptReady {
		m.status = Working
	} else if promptReady {
		m.status = Idle
	}
	return m.status
}

func screenPromptReady(screen string) bool {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	nonEmpty := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if len(nonEmpty) == 0 {
		return false
	}
	last := strings.TrimSpace(nonEmpty[len(nonEmpty)-1])
	if promptPattern.MatchString(last) {
		return true
	}
	if len(nonEmpty) < 2 || !footerPattern.MatchString(last) {
		return false
	}
	return promptPattern.MatchString(strings.TrimSpace(nonEmpty[len(nonEmpty)-2]))
}
