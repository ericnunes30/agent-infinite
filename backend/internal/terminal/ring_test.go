package terminal

import "testing"

func TestRingRetainsNewestBytesAtLimit(t *testing.T) {
	ring := NewRing(8)
	ring.Append([]byte("abc"))
	ring.Append([]byte("defghij"))
	if got := string(ring.Bytes()); got != "cdefghij" {
		t.Fatalf("Bytes() = %q, want %q", got, "cdefghij")
	}
}

func TestRingHandlesChunkLargerThanLimit(t *testing.T) {
	ring := NewRing(4)
	ring.Append([]byte("0123456789"))
	if got := string(ring.Bytes()); got != "6789" {
		t.Fatalf("Bytes() = %q, want %q", got, "6789")
	}
}
