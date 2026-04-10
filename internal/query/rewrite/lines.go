package rewrite

import (
	"fmt"
	"regexp"
	"strings"
)

var lineEnumPrefix = regexp.MustCompile(`^(?:\d+[\.\)、．]\s*|[（(]\d+[）)]\s*|子查询\s*\d+\s*[：:]\s*|第[一二三四五六七八九十]\s*[、．.]\s*)`)

func stripEnumerationPrefix(s string) string {
	s = strings.TrimSpace(s)
	for {
		t := lineEnumPrefix.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = strings.TrimSpace(t)
	}
	return s
}

// lineQuerySplitter 将流式片段按换行切分，每形成一行即回调（最多 maxEmit 次）。
type lineQuerySplitter struct {
	buf      strings.Builder
	onLine   func(string) error
	emitted  int
	maxEmit  int
}

func newLineQuerySplitter(onLine func(string) error, maxEmit int) *lineQuerySplitter {
	if maxEmit <= 0 {
		maxEmit = 5
	}
	return &lineQuerySplitter{onLine: onLine, maxEmit: maxEmit}
}

func (s *lineQuerySplitter) Write(piece string) error {
	s.buf.WriteString(piece)
	body := s.buf.String()
	s.buf.Reset()
	for {
		idx := strings.IndexByte(body, '\n')
		if idx < 0 {
			s.buf.WriteString(body)
			return nil
		}
		raw := body[:idx]
		body = body[idx+1:]
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		line = stripEnumerationPrefix(line)
		line = strings.TrimSpace(line)
		if line != "" && shouldStreamQueryLine(line) && s.onLine != nil && s.emitted < s.maxEmit {
			if err := s.onLine(line); err != nil {
				return err
			}
			s.emitted++
		}
	}
}

func (s *lineQuerySplitter) flushTail() error {
	rest := strings.TrimSpace(strings.TrimSuffix(s.buf.String(), "\r"))
	s.buf.Reset()
	rest = stripEnumerationPrefix(rest)
	rest = strings.TrimSpace(rest)
	if rest == "" || !shouldStreamQueryLine(rest) || s.onLine == nil || s.emitted >= s.maxEmit {
		return nil
	}
	return s.onLine(rest)
}

func tryParseLines(s string) []string {
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		line = stripEnumerationPrefix(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func looksLikeQueriesJSON(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.Contains(s, `"queries"`)
}

// shouldStreamQueryLine 为 false 时不触发 SSE（例如模型仍输出单行 JSON，由最终 parse 处理）。
func shouldStreamQueryLine(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) < 8 {
		return true
	}
	return !looksLikeQueriesJSON(line)
}

func parseQueriesFromModel(raw string) ([]string, error) {
	s := stripCodeFence(strings.TrimSpace(raw))
	if looksLikeQueriesJSON(s) {
		qs, err := parseQueriesJSON(s)
		if err == nil && len(qs) > 0 {
			return qs, nil
		}
	}
	lines := tryParseLines(s)
	if len(lines) > 0 {
		return lines, nil
	}
	if qs, err := parseQueriesJSON(s); err == nil && len(qs) > 0 {
		return qs, nil
	}
	return nil, fmt.Errorf("no queries in model output")
}
