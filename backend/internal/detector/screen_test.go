package detector

import (
	"strings"
	"testing"
)

func TestScreenStripsVTSequences(t *testing.T) {
	screen := NewScreen(40, 5)
	screen.Write([]byte("\x1b[31mhello\x1b[0m\r\n>"))
	text := screen.Text()
	if !strings.Contains(text, "hello") || strings.Contains(text, "\x1b") {
		t.Fatalf("Text() = %q", text)
	}
}
