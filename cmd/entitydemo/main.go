// Entitydemo：交互式试跑 internal/query/entity 关键词 / query 信号抽取。
//
// 用法示例：
//
//	go run ./cmd/entitydemo -mode article -top 8 "支持向量机是一种监督学习算法，广泛应用于分类和回归任务。"
//	go run ./cmd/entitydemo -mode query "BERT与SVM在文本分类上的区别"
//	go run ./cmd/entitydemo -i -mode article < article.txt
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ai-search-v1/internal/query/entity"
)

func main() {
	mode := flag.String("mode", "article", "article：长文关键词；query：检索 query 信号")
	top := flag.Int("top", 10, "输出条数（article 为关键词条数；query 中 Keywords 条数）")
	stdin := flag.Bool("i", false, "从标准输入读取全文（与位置参数、-t 互斥时优先）")
	textFlag := flag.String("t", "", "要分析的文本（可省略，改用位置参数）")
	flag.Parse()

	var text string
	if *stdin {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 stdin: %v\n", err)
			os.Exit(1)
		}
		text = string(b)
	} else if *textFlag != "" {
		text = *textFlag
	} else if a := flag.Args(); len(a) > 0 {
		text = strings.Join(a, " ")
	} else {
		fmt.Fprintf(os.Stderr, `用法:
  %s [-mode article|query] [-top N] "一段话..."
  %s -i [-mode article|query] < 文件.txt

参数:
  -mode article  长文 RAKE 关键词（默认）
  -mode query     短 query：实体 / 关系词 / 关键词
  -top N          输出条数上限
  -t 文本         直接跟字符串
  -i              从 stdin 读入全文
`, os.Args[0], os.Args[0])
		os.Exit(2)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		fmt.Fprintln(os.Stderr, "文本为空")
		os.Exit(1)
	}

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "article", "a":
		out := entity.ExtractKeywordsFromArticle(text, *top)
		fmt.Println("=== 关键词（RAKE）===")
		for i, r := range out {
			fmt.Printf("%d.\t%s\t(score=%.4f)\n", i+1, r.Phrase, r.Score)
		}
	case "query", "q":
		sig := entity.ExtractFromSearchQuery(text, *top)
		fmt.Println("=== 实体倾向 ===")
		for i, s := range sig.Entities {
			fmt.Printf("%d.\t%s\n", i+1, s)
		}
		fmt.Println("=== 关系提示词 ===")
		for i, s := range sig.Relations {
			fmt.Printf("%d.\t%s\n", i+1, s)
		}
		fmt.Println("=== 关键词（RAKE）===")
		for i, r := range sig.Keywords {
			fmt.Printf("%d.\t%s\t(score=%.4f)\n", i+1, r.Phrase, r.Score)
		}
	default:
		fmt.Fprintf(os.Stderr, "未知 -mode: %q（用 article 或 query）\n", *mode)
		os.Exit(1)
	}
}
