// Milvuspeek：连接配置中的 Milvus，抽样查询 collection 中的 chunk_id 与向量摘要（或按 chunk_id 精确查）。
// 构建：go build -o milvuspeek.exe ./cmd/evaluator/milvuspeek
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"ai-search-v1/internal/config"
	"ai-search-v1/internal/storage/milvus"
)

func main() {
	config.LoadDotEnv()

	cfgPath := flag.String("config", "", "api.yaml（默认 API_CONFIG 或 configs/api.yaml）")
	limit := flag.Int64("limit", 20, "Query 最大条数")
	expr := flag.String("expr", "", `查询表达式，默认 chunk_id != ""`)
	ids := flag.String("ids", "", "若非空：按逗号分隔的 chunk_id 精确查询，忽略 -expr/-limit")
	noVec := flag.Bool("no-vector", false, "不拉取向量，仅标量字段")
	flag.Parse()

	def := config.DefaultAPIConfigPath
	if *cfgPath == "" {
		*cfgPath = config.ResolveAPIConfigPath(def)
	}

	apiCfg, err := config.LoadAPI(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if !apiCfg.Milvus.MilvusEnabled() {
		log.Fatal("milvus.enabled 必须为 true")
	}
	mc := apiCfg.Milvus.ToMilvus()
	if err := mc.Validate(); err != nil {
		log.Fatalf("milvus config: %v", err)
	}

	ctx := context.Background()
	repo, err := milvus.NewRepository(ctx, mc)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer repo.Close()

	if err := repo.LoadCollection(ctx, false); err != nil {
		log.Fatalf("load collection: %v", err)
	}

	var rows []milvus.ChunkRecord
	if s := strings.TrimSpace(*ids); s != "" {
		parts := strings.Split(s, ",")
		chunkIDs := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				chunkIDs = append(chunkIDs, p)
			}
		}
		out := []string{milvus.FieldChunkID, milvus.FieldDocID, milvus.FieldSourceType, milvus.FieldLang, milvus.FieldJobID, milvus.FieldTaskID, milvus.FieldCreatedTime, milvus.FieldUpdatedTime}
		if !*noVec {
			out = append(out, milvus.FieldEmbedding)
		}
		rows, err = repo.QueryByChunkIDs(ctx, chunkIDs, out)
	} else {
		rows, err = repo.QueryByExpr(ctx, *expr, *limit, !*noVec)
	}
	if err != nil {
		log.Fatal(err)
	}

	if len(rows) == 0 {
		log.Print("no rows")
		return
	}

	for i := range rows {
		r := rows[i]
		fmt.Fprintf(os.Stdout, "--- [%d] chunk_id=%q doc_id=%q source_type=%q lang=%q job_id=%q task_id=%q created_ms=%d updated_ms=%d\n",
			i+1, r.ChunkID, r.DocID, r.SourceType, r.Lang, r.JobID, r.TaskID, r.CreatedTime, r.UpdatedTime)
		if len(r.Embedding) == 0 {
			fmt.Fprintln(os.Stdout, "    embedding: (empty or not requested)")
			continue
		}
		fmt.Fprintf(os.Stdout, "    embedding: dim=%d preview=%s\n", len(r.Embedding), formatVecPreview(r.Embedding, 8))
	}
}

func formatVecPreview(v []float32, n int) string {
	if n > len(v) {
		n = len(v)
	}
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("%.6f", v[i]))
	}
	s := strings.Join(parts, ", ")
	if len(v) > n {
		s += ", ..."
	}
	return "[" + s + "]"
}
