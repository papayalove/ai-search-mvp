package pipeline

import "testing"

func TestEffectiveIngestSourceType(t *testing.T) {
	if g := EffectiveIngestSourceType(".txt", "md"); g != "txt" {
		t.Fatalf("want txt from ext, got %q", g)
	}
	if g := EffectiveIngestSourceType(".md", "pdf"); g != "md" {
		t.Fatalf("want md from ext, got %q", g)
	}
	if g := EffectiveIngestSourceType("", "kb"); g != "kb" {
		t.Fatalf("want job fallback kb, got %q", g)
	}
	if g := EffectiveIngestSourceType("", ""); g != "default" {
		t.Fatalf("want default, got %q", g)
	}
	if g := EffectiveIngestSourceType(".unknown", "x"); g != "x" {
		t.Fatalf("want job fallback x, got %q", g)
	}
}
