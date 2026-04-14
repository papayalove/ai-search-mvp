package entity

import "strings"

// 停用词用于 RAKE：在词序列中打断候选短语边界（中英混合文章 / 查询）。
var rakeStopwords = map[string]struct{}{
	// English
	"a": {}, "about": {}, "above": {}, "after": {}, "again": {}, "against": {}, "all": {}, "am": {}, "an": {},
	"and": {}, "any": {}, "are": {}, "aren't": {}, "as": {}, "at": {}, "be": {}, "because": {}, "been": {},
	"before": {}, "being": {}, "below": {}, "between": {}, "both": {}, "but": {}, "by": {}, "can't": {},
	"cannot": {}, "could": {}, "couldn't": {}, "did": {}, "didn't": {}, "do": {}, "does": {}, "doesn't": {},
	"doing": {}, "don't": {}, "down": {}, "during": {}, "each": {}, "few": {}, "for": {}, "from": {},
	"further": {}, "had": {}, "hadn't": {}, "has": {}, "hasn't": {}, "have": {}, "haven't": {}, "having": {},
	"he": {}, "he'd": {}, "he'll": {}, "he's": {}, "her": {}, "here": {}, "here's": {}, "hers": {}, "herself": {},
	"him": {}, "himself": {}, "his": {}, "how": {}, "how's": {}, "i": {}, "i'd": {}, "i'll": {}, "i'm": {},
	"i've": {}, "if": {}, "in": {}, "into": {}, "is": {}, "isn't": {}, "it": {}, "it's": {}, "its": {},
	"itself": {}, "let's": {}, "me": {}, "more": {}, "most": {}, "mustn't": {}, "my": {}, "myself": {}, "no": {},
	"nor": {}, "not": {}, "of": {}, "off": {}, "on": {}, "once": {}, "only": {}, "or": {}, "other": {}, "ought": {},
	"our": {}, "ours": {}, "ourselves": {}, "out": {}, "over": {}, "own": {}, "same": {}, "shan't": {}, "she": {},
	"she'd": {}, "she'll": {}, "she's": {}, "should": {}, "shouldn't": {}, "so": {}, "some": {}, "such": {},
	"than": {}, "that": {}, "that's": {}, "the": {}, "their": {}, "theirs": {}, "them": {}, "themselves": {},
	"then": {}, "there": {}, "there's": {}, "these": {}, "they": {}, "they'd": {}, "they'll": {}, "they're": {},
	"they've": {}, "this": {}, "those": {}, "through": {}, "to": {}, "too": {}, "under": {}, "until": {}, "up": {},
	"very": {}, "was": {}, "wasn't": {}, "we": {}, "we'd": {}, "we'll": {}, "we're": {}, "we've": {}, "were": {},
	"weren't": {}, "what": {}, "what's": {}, "when": {}, "when's": {}, "where": {}, "where's": {}, "which": {},
	"while": {}, "who": {}, "who's": {}, "whom": {}, "why": {}, "why's": {}, "with": {}, "won't": {}, "would": {},
	"wouldn't": {}, "you": {}, "you'd": {}, "you'll": {}, "you're": {}, "you've": {}, "your": {}, "yours": {},
	"yourself": {}, "yourselves": {},
	// Chinese（常见虚词 / 结构词）
	"的": {}, "了": {}, "和": {}, "与": {}, "或": {}, "及": {}, "等": {}, "在": {}, "是": {}, "有": {}, "被": {},
	"为": {}, "以": {}, "所": {}, "将": {}, "对": {}, "从": {}, "到": {}, "向": {}, "于": {}, "把": {}, "让": {},
	"给": {}, "由": {}, "而": {}, "且": {}, "但": {}, "却": {}, "若": {}, "如": {}, "虽": {}, "因": {}, "所以": {},
	"如果": {}, "因为": {}, "然而": {}, "因此": {}, "以及": {}, "及其": {}, "或者": {}, "而且": {}, "但是": {},
	"一个": {}, "一种": {}, "一些": {}, "这个": {}, "那个": {}, "这些": {}, "那些": {}, "这样": {}, "那样": {},
	"可以": {}, "能够": {}, "应该": {}, "已经": {}, "只是": {}, "就是": {}, "不是": {},
	"没有": {}, "不能": {}, "不会": {}, "需要": {}, "通过": {}, "根据": {}, "关于": {}, "对于": {},
	"其中": {}, "之一": {}, "之间": {}, "之后": {}, "之前": {}, "以上": {}, "以下": {}, "上的": {}, "中的": {},
	"非常": {}, "比较": {},
	"更加": {}, "主要": {}, "一般": {}, "通常": {},
}

func isRakeStopword(w string) bool {
	if w == "" {
		return true
	}
	_, ok := rakeStopwords[w]
	return ok
}

// 查询里常作关系提示的词（FromQuery 的 Relations 字段）。
var relationCueWords = map[string]struct{}{
	// Chinese
	"对比": {}, "比较": {}, "区别": {}, "差异": {}, "关系": {}, "影响": {}, "相关": {}, "相似": {}, "相反": {},
	"之间": {}, "和": {}, "与": {}, "或": {}, "以及": {}, "相对": {}, "针对": {}, "关于": {},
	// English
	"vs": {}, "versus": {}, "compare": {}, "compared": {}, "between": {}, "relation": {}, "relationship": {},
	"similar": {}, "different": {}, "difference": {}, "and": {}, "or": {}, "with": {}, "without": {},
}

func isRelationCue(w string) bool {
	_, ok := relationCueWords[stringsToLowerASCII(w)]
	return ok
}

// stringsToLowerASCII 仅对 ASCII 字母做小写，其余 rune 原样（中英混合 key；按 rune 迭代保证 UTF-8 安全）。
func stringsToLowerASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
