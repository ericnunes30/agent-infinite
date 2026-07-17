package detector

import (
	"sync"

	"github.com/maximhq/vt10x"
)

type Screen struct {
	mu       sync.Mutex
	terminal vt10x.Terminal
}

func NewScreen(cols, rows int) *Screen {
	return &Screen{terminal: vt10x.New(vt10x.WithSize(cols, rows))}
}

func (s *Screen) Write(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.terminal.Write(data)
}

func (s *Screen) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal.String()
}

func (s *Screen) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminal.Resize(cols, rows)
}
