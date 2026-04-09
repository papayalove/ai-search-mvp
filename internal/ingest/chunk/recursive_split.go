package chunk

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// RecursiveChunkOptions 对齐 Python RecursiveCharacterTextSplitter（chunk_size / chunk_overlap / separators / keep_separator / len）。
type RecursiveChunkOptions struct {
	ChunkSize     int
	ChunkOverlap  int
	Separators    []string
	KeepSeparator bool
	// Len 为 nil 时使用 utf8.RuneCountInString，与 Python len(str) 在 Unicode 文本上一致。
	Len func(string) int
}

// DefaultRecursiveSeparators 与给定 Python 示例顺序一致。
func DefaultRecursiveSeparators() []string {
	return []string{
		"\n\n", "\n", ".", "?", "!", "。", "？", "！", " ", "",
	}
}

// ChunkTextRecursively 对全文做小数点保护 → 递归字符切分 → 还原小数点 → 去空白并丢弃空串。
func ChunkTextRecursively(text string, opts RecursiveChunkOptions) ([]string, error) {
	size := opts.ChunkSize
	if size <= 0 {
		size = 768
	}
	overlap := opts.ChunkOverlap
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		return nil, fmt.Errorf("chunk_overlap (%d) must be less than chunk_size (%d)", overlap, size)
	}
	seps := opts.Separators
	if len(seps) == 0 {
		seps = DefaultRecursiveSeparators()
	}
	lenFn := opts.Len
	if lenFn == nil {
		lenFn = utf8.RuneCountInString
	}

	protected := protectDecimalPeriods(text)
	s := &recursiveSplitter{
		chunkSize:    size,
		chunkOverlap: overlap,
		keepSep:      opts.KeepSeparator,
		len:          lenFn,
	}
	raw, err := s.splitText(protected, seps)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		u := strings.TrimSpace(unprotectDecimalPeriods(c))
		if u != "" {
			out = append(out, u)
		}
	}
	return out, nil
}

type recursiveSplitter struct {
	chunkSize    int
	chunkOverlap int
	keepSep      bool
	len          func(string) int
}

// 合并与递归流程参考 LangChain RecursiveCharacterTextSplitter 与 github.com/tmc/langchaingo/textsplitter（MIT）。
func (s *recursiveSplitter) splitText(text string, separators []string) ([]string, error) {
	finalChunks := make([]string, 0)
	separator := separators[len(separators)-1]
	newSeparators := []string{}
	for i := range separators {
		c := separators[i]
		if c == "" || strings.Contains(text, c) {
			separator = c
			if i+1 <= len(separators) {
				newSeparators = separators[i+1:]
			}
			break
		}
	}

	var splits []string
	if separator == "" {
		splits = splitByRunes(text)
	} else {
		splits = strings.Split(text, separator)
		if s.keepSep {
			splits = addSeparatorPrefixToSplits(splits, separator)
			separator = ""
		}
	}

	goodSplits := make([]string, 0)
	for _, part := range splits {
		if s.len(part) < s.chunkSize {
			goodSplits = append(goodSplits, part)
			continue
		}
		if len(goodSplits) > 0 {
			merged := mergeSplits(goodSplits, separator, s.chunkSize, s.chunkOverlap, s.len)
			finalChunks = append(finalChunks, merged...)
			goodSplits = goodSplits[:0]
		}
		if len(newSeparators) == 0 {
			finalChunks = append(finalChunks, part)
		} else {
			other, err := s.splitText(part, newSeparators)
			if err != nil {
				return nil, err
			}
			finalChunks = append(finalChunks, other...)
		}
	}
	if len(goodSplits) > 0 {
		merged := mergeSplits(goodSplits, separator, s.chunkSize, s.chunkOverlap, s.len)
		finalChunks = append(finalChunks, merged...)
	}
	return finalChunks, nil
}

func addSeparatorPrefixToSplits(splits []string, separator string) []string {
	out := make([]string, 0, len(splits))
	for i, p := range splits {
		if i > 0 {
			p = separator + p
		}
		out = append(out, p)
	}
	return out
}

func splitByRunes(s string) []string {
	rr := []rune(s)
	out := make([]string, len(rr))
	for i, r := range rr {
		out[i] = string(r)
	}
	return out
}

func mergeSplits(splits []string, separator string, chunkSize, chunkOverlap int, lenFunc func(string) int) []string {
	docs := make([]string, 0)
	currentDoc := make([]string, 0)
	total := 0

	for _, split := range splits {
		totalWithSplit := total + lenFunc(split)
		if len(currentDoc) != 0 {
			totalWithSplit += lenFunc(separator)
		}

		if totalWithSplit > chunkSize && len(currentDoc) > 0 {
			doc := joinDocs(currentDoc, separator)
			if doc != "" {
				docs = append(docs, doc)
			}
			for shouldPopChunk(chunkOverlap, chunkSize, total, lenFunc(split), lenFunc(separator), len(currentDoc)) {
				total -= lenFunc(currentDoc[0])
				if len(currentDoc) > 1 {
					total -= lenFunc(separator)
				}
				currentDoc = currentDoc[1:]
			}
		}

		currentDoc = append(currentDoc, split)
		total += lenFunc(split)
		if len(currentDoc) > 1 {
			total += lenFunc(separator)
		}
	}

	doc := joinDocs(currentDoc, separator)
	if doc != "" {
		docs = append(docs, doc)
	}
	return docs
}

func joinDocs(docs []string, separator string) string {
	return strings.TrimSpace(strings.Join(docs, separator))
}

func shouldPopChunk(chunkOverlap, chunkSize, total, splitLen, separatorLen, currentDocLen int) bool {
	if currentDocLen <= 0 {
		return false
	}
	if currentDocLen < 2 {
		separatorLen = 0
	}
	return total > chunkOverlap || (total+splitLen+separatorLen > chunkSize && total > 0)
}
