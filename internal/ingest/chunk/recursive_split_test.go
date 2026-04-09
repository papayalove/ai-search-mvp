package chunk

import (
	"strings"
	"testing"
)

func TestProtectDecimalPeriods(t *testing.T) {
	in := "pi 为 3.14。下一句"
	p := protectDecimalPeriods(in)
	if strings.Contains(p, "3.14") {
		t.Fatalf("dot should be protected: %q", p)
	}
	u := unprotectDecimalPeriods(p)
	if !strings.Contains(u, "3.14") {
		t.Fatalf("restore: %q", u)
	}
}

func TestChunkTextRecursively_Newlines(t *testing.T) {
	text := strings.Repeat("字", 100) + "\n\n" + strings.Repeat("a", 100)
	chunks, err := ChunkTextRecursively(text, RecursiveChunkOptions{
		ChunkSize:     120,
		ChunkOverlap:  16,
		KeepSeparator: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %#v", len(chunks), chunks)
	}
}

func TestChunkTextRecursively_Defaults(t *testing.T) {
	_, err := ChunkTextRecursively("short", RecursiveChunkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ChunkTextRecursively("x", RecursiveChunkOptions{ChunkSize: 10, ChunkOverlap: 10})
	if err == nil {
		t.Fatal("want error when overlap >= size")
	}
}
