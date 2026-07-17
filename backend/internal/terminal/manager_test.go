package terminal

import (
	"strings"
	"testing"
)

func TestPreviewTrimsBlankViewportAndKeepsUnicodeTail(t *testing.T) {
	got := preview("old\nMOCK_DONE: ação"+strings.Repeat(" ", 500), 16)
	if got != "\nMOCK_DONE: ação" {
		t.Fatalf("preview() = %q", got)
	}
}
