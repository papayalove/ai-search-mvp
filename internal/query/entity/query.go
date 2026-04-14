package entity

import (
	"regexp"
	"strings"
	"unicode"
)

// QuerySignals 搜索 query 的结构化信号：实体倾向片段、关系提示词、RAKE 关键词。
type QuerySignals struct {
	Entities  []string
	Relations []string
	Keywords  []RankedPhrase
}

var (
	reQuoted     = regexp.MustCompile(`(?s)["'「『](.+?)["'」』]`)
	reKV         = regexp.MustCompile(`(?i)\b([a-z_]{1,32})\s*[:：]\s*(\S+)`)
	reCamelToken = regexp.MustCompile(`\b[A-Z][a-z]+(?:[A-Z][a-z]+)+\b`)
)

// ExtractFromSearchQuery 从短 query 抽取：实体倾向片段、关系类提示词、以及 RAKE 排名关键词。
func ExtractFromSearchQuery(q string, topN int) QuerySignals {
	q = strings.TrimSpace(q)
	if q == "" {
		return QuerySignals{}
	}
	if topN <= 0 {
		topN = 8
	}

	seenEnt := make(map[string]struct{})
	seenRel := make(map[string]struct{})
	var entities []string
	var relations []string

	addEnt := func(s string) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 2 && len(s) < 2 {
			return
		}
		key := strings.ToLower(s)
		if _, ok := seenEnt[key]; ok {
			return
		}
		seenEnt[key] = struct{}{}
		entities = append(entities, s)
	}
	addRel := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		key := stringsToLowerASCII(s)
		if _, ok := seenRel[key]; ok {
			return
		}
		seenRel[key] = struct{}{}
		relations = append(relations, s)
	}

	for _, m := range reQuoted.FindAllStringSubmatch(q, -1) {
		if len(m) > 1 {
			addEnt(m[1])
		}
	}
	for _, m := range reKV.FindAllStringSubmatch(q, -1) {
		if len(m) > 2 {
			addEnt(m[2])
		}
	}
	for _, m := range reCamelToken.FindAllString(q, -1) {
		addEnt(m)
	}

	for _, raw := range rawTokens(q) {
		if isRelationCue(raw) {
			addRel(raw)
		}
		r := strings.TrimSpace(raw)
		if r == "" {
			continue
		}
		if isRakeStopword(strings.ToLower(r)) {
			continue
		}
		// 汉字连续串（>=2）视为实体候选；先按句内中文停用词切开，避免「在…上的区别」整段粘连。
		if hanLen(r) >= 2 && allHan(r) {
			parts := splitHanAtStopwords(r)
			if len(parts) == 0 {
				addEnt(r)
			} else {
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if len([]rune(p)) < 2 || isRakeStopword(strings.ToLower(p)) {
						continue
					}
					addEnt(p)
				}
			}
			continue
		}
		// 拉丁 / 数字 token：长度阈值，排除纯关系小词
		if latinDigitLen(r) >= 2 && !isRelationCue(r) {
			addEnt(r)
		}
	}

	kw := ExtractKeywordsFromArticle(q, topN)
	return QuerySignals{
		Entities:  entities,
		Relations: relations,
		Keywords:  kw,
	}
}

func allHan(s string) bool {
	for _, r := range s {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

func hanLen(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}

func latinDigitLen(s string) int {
	n := 0
	for _, r := range s {
		if isLatinDigitRune(r) {
			n++
		}
	}
	return n
}
