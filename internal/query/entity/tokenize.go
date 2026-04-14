package entity

import (
	"sort"
	"strings"
	"unicode"
)

// chineseStopList 仅含「全汉字」的停用词，按长度降序，用于在汉字串内切分短语边界。
var chineseStopList []string

// 句内切分用的单字（避免把「向量机」「应用于」切碎；不包含「向」「于」等易嵌入专业词的虚字）。
var chineseStopInfixSingle = []rune{'的', '了', '是', '在', '和', '与', '或', '及', '但', '而', '被', '把', '让', '给', '以', '为', '所', '将', '对', '从', '到'}

func init() {
	for w := range rakeStopwords {
		if w == "" {
			continue
		}
		allH := true
		for _, r := range w {
			if !unicode.Is(unicode.Han, r) {
				allH = false
				break
			}
		}
		if !allH {
			continue
		}
		// 仅多字停用词参与最长匹配，单字仅用白名单，减少误切专业术语。
		if len([]rune(w)) >= 2 {
			chineseStopList = append(chineseStopList, w)
		}
	}
	for _, r := range chineseStopInfixSingle {
		chineseStopList = append(chineseStopList, string(r))
	}
	sort.Slice(chineseStopList, func(i, j int) bool {
		return len([]rune(chineseStopList[i])) > len([]rune(chineseStopList[j]))
	})
}

// splitHanAtStopwords 在汉字串内按中文停用词切分（最长匹配），去掉匹配到的停用词本身。
func splitHanAtStopwords(s string) []string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) == 0 {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(rs); {
		best := 0
		for _, sw := range chineseStopList {
			swr := []rune(sw)
			if len(swr) == 0 {
				continue
			}
			if i+len(swr) <= len(rs) && string(rs[i:i+len(swr)]) == sw && len(swr) > best {
				best = len(swr)
			}
		}
		if best > 0 {
			if i > start {
				parts = append(parts, string(rs[start:i]))
			}
			i += best
			start = i
			continue
		}
		i++
	}
	if start < len(rs) {
		parts = append(parts, string(rs[start:]))
	}
	return parts
}

// sentenceSplitRunes 按句号类标点切句（保留句内内容）。
func sentenceSplitRunes(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		if r == '。' || r == '！' || r == '？' || r == '.' || r == '!' || r == '?' || r == '\n' {
			t := strings.TrimSpace(b.String())
			if t != "" {
				out = append(out, t)
			}
			b.Reset()
		}
	}
	if tail := strings.TrimSpace(b.String()); tail != "" {
		out = append(out, tail)
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

func isLatinDigitRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isHanRune(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// rawTokens 将一句拆成：连续英文/数字段、连续汉字段、其余单字符标点等。
func rawTokens(sentence string) []string {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return nil
	}
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		buf := cur.String()
		cur.Reset()
		rs := []rune(buf)
		if len(rs) > 0 && isHanRune(rs[0]) {
			parts := splitHanAtStopwords(buf)
			if len(parts) == 0 {
				// 整段被「单字停用」占满（如单独一个「与」）时仍保留原 token，供 query 关系词等逻辑使用。
				toks = append(toks, buf)
				return
			}
			for _, part := range parts {
				p := strings.TrimSpace(part)
				if p != "" {
					toks = append(toks, p)
				}
			}
			return
		}
		toks = append(toks, buf)
	}
	var prevClass int8 // 0 none, 1 latin/digit, 2 han
	for _, r := range sentence {
		var cls int8
		switch {
		case isLatinDigitRune(r):
			cls = 1
		case isHanRune(r):
			cls = 2
		default:
			cls = 0
		}
		if cls == 0 {
			flush()
			continue
		}
		if prevClass != 0 && cls != prevClass {
			flush()
		}
		prevClass = cls
		cur.WriteRune(r)
	}
	flush()
	return toks
}

const maxHanRakeUnit = 8

// rakeWordsFromRaw 把 raw token（英文数字块或汉字块）拆成 RAKE 统计用的「词」。
func rakeWordsFromRaw(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	rs := []rune(raw)
	if len(rs) == 0 {
		return nil
	}
	if isHanRune(rs[0]) {
		return splitHanRakeUnits(string(rs))
	}
	return []string{strings.ToLower(raw)}
}

func splitHanRakeUnits(s string) []string {
	rs := []rune(s)
	if len(rs) == 0 {
		return nil
	}
	if len(rs) <= maxHanRakeUnit {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(rs); {
		j := i + maxHanRakeUnit
		if j > len(rs) {
			j = len(rs)
		}
		out = append(out, string(rs[i:j]))
		i = j
	}
	return out
}

func candidatePhrasesFromText(text string) []phraseWords {
	var all []phraseWords
	for _, sent := range sentenceSplitRunes(text) {
		all = append(all, candidatePhrasesFromSentence(sent)...)
	}
	return mergeAdjacentSingletons(all)
}

// phraseWords 一个候选短语内的词序列（用于 RAKE 计分）。
type phraseWords struct {
	words []string
}

func isHanRawToken(raw string) bool {
	rs := []rune(strings.TrimSpace(raw))
	return len(rs) > 0 && isHanRune(rs[0])
}

func candidatePhrasesFromSentence(sentence string) []phraseWords {
	var out []phraseWords
	var cur []string
	raws := rawTokens(sentence)
	for ri, raw := range raws {
		units := rakeWordsFromRaw(raw)
		if len(units) > 1 {
			if len(cur) > 0 {
				out = append(out, phraseWords{words: append([]string(nil), cur...)})
				cur = cur[:0]
			}
			for _, w := range units {
				k := strings.TrimSpace(w)
				if k == "" || isRakeStopword(strings.ToLower(k)) {
					continue
				}
				out = append(out, phraseWords{words: []string{k}})
			}
			continue
		}
		for _, w := range units {
			k := strings.TrimSpace(w)
			if k == "" {
				continue
			}
			if isRakeStopword(strings.ToLower(k)) {
				if len(cur) > 0 {
					out = append(out, phraseWords{words: append([]string(nil), cur...)})
					cur = cur[:0]
				}
				continue
			}
			cur = append(cur, k)
		}
		// 相邻汉字块（如逗号两侧）各自成短语，避免整句粘成一条 RAKE 候选。
		if len(cur) > 0 && isHanRawToken(raw) && ri+1 < len(raws) && isHanRawToken(raws[ri+1]) {
			out = append(out, phraseWords{words: append([]string(nil), cur...)})
			cur = cur[:0]
		}
	}
	if len(cur) > 0 {
		out = append(out, phraseWords{words: append([]string(nil), cur...)})
	}
	return out
}

// mergeAdjacentSingletons 合并相邻的单字英文碎片（如 s v m）——对中文场景影响小，对缩写友好。
func mergeAdjacentSingletons(in []phraseWords) []phraseWords {
	if len(in) <= 1 {
		return in
	}
	var out []phraseWords
	i := 0
	for i < len(in) {
		words := append([]string(nil), in[i].words...)
		j := i + 1
		for j < len(in) && len(in[j].words) == 1 && len(words) >= 1 {
			last := words[len(words)-1]
			nxt := in[j].words[0]
			if len([]rune(last)) == 1 && len([]rune(nxt)) == 1 && isLatinDigitRune([]rune(last)[0]) && isLatinDigitRune([]rune(nxt)[0]) {
				words[len(words)-1] = last + nxt
				j++
				continue
			}
			break
		}
		out = append(out, phraseWords{words: words})
		i = j
	}
	return out
}

func phraseDisplay(pw phraseWords) string {
	if len(pw.words) == 0 {
		return ""
	}
	if len(pw.words) == 1 {
		return pw.words[0]
	}
	// 若全是汉字，拼接无空格；否则用空格连接拉丁词。
	allHan := true
	for _, w := range pw.words {
		for _, r := range w {
			if !isHanRune(r) {
				allHan = false
				break
			}
		}
		if !allHan {
			break
		}
	}
	if allHan {
		return strings.Join(pw.words, "")
	}
	return strings.Join(pw.words, " ")
}
