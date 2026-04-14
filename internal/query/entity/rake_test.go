package entity

import (
	"strings"
	"testing"
)

func TestExtractKeywordsFromArticle_SVMChinese(t *testing.T) {
	text := "支持向量机是一种监督学习算法，广泛应用于分类和回归任务。"
	got := ExtractKeywordsFromArticle(text, 5)
	if len(got) == 0 {
		t.Fatal("expected keywords")
	}
	joined := strings.Join(PhrasesOnly(got), ",")
	t.Logf("top: %v", got)
	if !strings.Contains(joined, "支持向量机") {
		t.Fatalf("missing 支持向量机 in %q", joined)
	}
}

func TestExtractFromSearchQuery(t *testing.T) {
	q := `BERT与SVM在文本分类上的区别`
	sig := ExtractFromSearchQuery(q, 6)
	t.Logf("entities=%v relations=%v kw=%v", sig.Entities, sig.Relations, sig.Keywords)
	if len(sig.Keywords) == 0 {
		t.Fatal("expected keywords")
	}
	foundRel := false
	for _, r := range sig.Relations {
		if r == "与" || r == "区别" || r == "和" {
			foundRel = true
			break
		}
	}
	if !foundRel {
		t.Fatalf("expected relation cue, got relations=%v", sig.Relations)
	}
}

func TestCandidatePhrases_Empty(t *testing.T) {
	if x := ExtractKeywordsFromArticle("   ", 5); len(x) != 0 {
		t.Fatalf("want empty, got %v", x)
	}
}
