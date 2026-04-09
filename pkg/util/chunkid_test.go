package util

import "testing"

func TestStableChunkID_deterministic(t *testing.T) {
	a := StableChunkID("doc-a", 1, 2)
	b := StableChunkID("doc-a", 1, 2)
	if a != b || len(a) != 64 {
		t.Fatalf("got %q len=%d", a, len(a))
	}
	if StableChunkID("doc-a", 1, 3) == a {
		t.Fatal("expected different chunk_no to change id")
	}
}
