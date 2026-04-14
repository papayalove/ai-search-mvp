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

func TestStableDocIDFromS3Object(t *testing.T) {
	a := StableDocIDFromS3Object("b", "k/x.md")
	b := StableDocIDFromS3Object("b", "/k/x.md")
	if a != b || len(a) != 64 {
		t.Fatalf("got %q len=%d", a, len(a))
	}
	if StableDocIDFromS3Object("b2", "k/x.md") == a {
		t.Fatal("expected different bucket to change id")
	}
}
