package entity

import (
	"math"
	"sort"
	"strings"
)

// RankedPhrase 带分数的短语（分数越高越重要，算法同 RAKE：词度/词频加权求和）。
type RankedPhrase struct {
	Phrase string
	Score    float64
}

const defaultMinPhraseTokens = 1

// ExtractKeywordsFromArticle 从正文抽取关键词/关键短语（RAKE 风格，适合多句长文）。
func ExtractKeywordsFromArticle(text string, topN int) []RankedPhrase {
	if topN <= 0 {
		topN = 10
	}
	phrases := candidatePhrasesFromText(text)
	scored := scorePhrases(phrases, defaultMinPhraseTokens)
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return len(scored[i].Phrase) > len(scored[j].Phrase)
		}
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > topN {
		scored = scored[:topN]
	}
	return scored
}

func scorePhrases(phrases []phraseWords, minTok int) []RankedPhrase {
	if minTok < 1 {
		minTok = 1
	}
	var usable []phraseWords
	for _, p := range phrases {
		if len(p.words) >= minTok {
			usable = append(usable, p)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	wordFreq := map[string]int{}
	wordDeg := map[string]int{}
	for _, p := range usable {
		lw := len(p.words)
		if lw == 0 {
			continue
		}
		for _, w := range p.words {
			wordFreq[w]++
			wordDeg[w] += lw
		}
	}
	wordScore := map[string]float64{}
	for w, f := range wordFreq {
		if f <= 0 {
			continue
		}
		wordScore[w] = float64(wordDeg[w]) / float64(f)
	}
	phraseAgg := map[string]float64{}
	for _, p := range usable {
		key := phraseDisplay(p)
		if key == "" {
			continue
		}
		var s float64
		for _, w := range p.words {
			s += wordScore[w]
		}
		if math.IsNaN(s) || math.IsInf(s, 0) {
			continue
		}
		phraseAgg[key] += s
	}
	out := make([]RankedPhrase, 0, len(phraseAgg))
	for ph, sc := range phraseAgg {
		out = append(out, RankedPhrase{Phrase: ph, Score: sc})
	}
	return out
}

// PhrasesOnly 仅返回短语文本，顺序与 ExtractKeywordsFromArticle 一致。
func PhrasesOnly(ranked []RankedPhrase) []string {
	if len(ranked) == 0 {
		return nil
	}
	out := make([]string, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].Phrase
	}
	return out
}

// JoinRankedPhrases 便于日志 / 调试。
func JoinRankedPhrases(ranked []RankedPhrase, sep string) string {
	if len(ranked) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range ranked {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(r.Phrase)
	}
	return b.String()
}
