package chunk

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitRecord_ChunkOverlap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 120; i++ {
		sb.WriteString("这是第")
		sb.WriteString(fmt.Sprintf("%d", i))
		sb.WriteString("句。放在同一段里用于触发多块重叠。")
	}
	text := sb.String()
	rec := TextChunkLine{ChunkID: "x", Text: text, DocID: "d", PageNo: 0, ChunkNo: 0}
	opts := RecursiveChunkOptions{ChunkSize: 100, ChunkOverlap: 30, KeepSeparator: true}
	_, err := SplitRecord(rec, opts)
	if err != nil {
		t.Fatalf("SplitRecord with overlap: %v", err)
	}
}
