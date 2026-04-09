// Importer：-input 时单次 NDJSON 文件导入；无 -input 时作为队列 Worker 消费 Redis。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ai-search-v1/internal/config"
	"ai-search-v1/internal/ingest/chunk"
	"ai-search-v1/internal/ingest/pipeline"
	"ai-search-v1/internal/queue"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"
	storages3 "ai-search-v1/internal/storage/s3"
)

func main() {
	config.LoadDotEnv()

	configPath := flag.String("config", "", "api.yaml 路径（默认 API_CONFIG 或 configs/api.yaml）")
	inputPath := flag.String("input", "", "NDJSON 输入文件；留空则启动队列 Worker（需 REDIS_INGEST_URL 或 REDIS_INGEST_HOST）")
	partition := flag.String("partition", "", "Milvus 分区名（可选，仅 -input 模式）")
	upsert := flag.Bool("upsert", false, "使用 Upsert 代替 Insert")
	ensure := flag.Bool("ensure-collection", true, "若集合不存在则创建、建索引并 Load")
	noFlush := flag.Bool("no-flush", false, "导入结束后不执行 Flush（仅 -input）")
	dryRun := flag.Bool("dry-run", false, "只解析输入并统计行数（需 -input）")
	chunkExpand := flag.Bool("chunk", false, "递归切分后再嵌入")
	chunkSizeF := flag.Int("chunk-size", -1, "覆盖 ingest.chunk_size，-1 使用配置文件")
	chunkOverlapF := flag.Int("chunk-overlap", -1, "覆盖 ingest.chunk_overlap，-1 使用配置文件")
	flag.Parse()

	def := config.DefaultAPIConfigPath
	if *configPath == "" {
		*configPath = config.ResolveAPIConfigPath(def)
	}

	apiCfg, err := config.LoadAPI(*configPath)
	if err != nil {
		log.Fatalf("load config %q: %v", *configPath, err)
	}

	ropts := apiCfg.Ingest.ToRecursiveChunkOptions()
	if *chunkSizeF >= 0 {
		ropts.ChunkSize = *chunkSizeF
	}
	if *chunkOverlapF >= 0 {
		ropts.ChunkOverlap = *chunkOverlapF
	}

	if *inputPath == "" {
		if *dryRun {
			log.Fatal("dry-run requires -input")
		}
		runWorker(apiCfg, ropts, *ensure)
		return
	}

	runOnce(apiCfg, ropts, *inputPath, *partition, *upsert, *ensure, *noFlush, *dryRun, *chunkExpand)
}

func runWorker(apiCfg *config.API, ropts chunk.RecursiveChunkOptions, ensureCol bool) {
	qe := config.LoadIngestQueueFromEnv()
	if qe.RedisURL == "" {
		wd, _ := os.Getwd()
		log.Fatalf("worker mode: 未解析到 Redis 连接。请在仓库根目录 .env 中设置 REDIS_INGEST_HOST+REDIS_INGEST_PORT 或 REDIS_INGEST_URL；当前工作目录=%q（已从该目录向上查找 go.mod 并加载对应根目录下的 .env）", wd)
	}
	if !qe.Enabled {
		log.Fatal("worker mode: 队列已关闭（REDIS_INGEST_ENABLED=false 或 INGEST_QUEUE_ENABLED=false）。改为 true 或删去该变量后重试")
	}
	broker, err := queue.NewRedisBroker(qe.RedisURL, qe.QueueListKey)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer broker.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if !apiCfg.Milvus.MilvusEnabled() {
		log.Fatal("milvus.enabled 必须为 true")
	}
	mc := apiCfg.Milvus.ToMilvus()
	if err := mc.Validate(); err != nil {
		log.Fatalf("milvus config: %v", err)
	}
	emb, err := apiCfg.BuildEmbedder()
	if err != nil {
		log.Fatalf("embedding: %v", err)
	}
	if emb == nil {
		log.Fatal("embedding.enabled: true 且配置 http/local")
	}
	logMilvusDialDebug(mc)

	repo, err := milvus.NewRepository(ctx, mc)
	if err != nil {
		log.Fatalf("milvus connect: %v", err)
	}
	defer repo.Close()
	if ensureCol {
		if err := repo.EnsureCollection(ctx); err != nil {
			log.Fatalf("ensure collection: %v", err)
		}
		log.Printf("collection %q ready", mc.Collection)
	}

	var esRepo *es.Repository
	if apiCfg.Elasticsearch.ElasticsearchEnabled() {
		ec := apiCfg.Elasticsearch.ToElasticsearch()
		if err := ec.Validate(); err != nil {
			log.Fatalf("elasticsearch: %v", err)
		}
		er, err := es.NewRepository(ec)
		if err != nil {
			log.Fatalf("elasticsearch client: %v", err)
		}
		if err := er.EnsureIndex(ctx); err != nil {
			log.Fatalf("elasticsearch ensure index: %v", err)
		}
		esRepo = er
		log.Printf("elasticsearch index %q ready", ec.Index)
	}

	s3cli, err := storages3.New(ctx, qe.S3)
	if err != nil {
		log.Fatalf("s3 client: %v", err)
	}

	runner := &pipeline.Runner{
		Embedder: emb,
		Repo:     repo,
		ES:       esRepo,
		MaxBatch: apiCfg.Embedding.MaxBatch,
	}
	worker := &pipeline.JobWorker{Runner: runner, Broker: broker, S3: s3cli}

	log.Printf("importer worker listening on redis list %q", qe.QueueListKey)
	for {
		j, err := broker.Dequeue(ctx, 0)
		if err != nil {
			if ctx.Err() != nil {
				log.Print("worker shutdown")
				return
			}
			log.Printf("dequeue error: %v", err)
			continue
		}
		_ = broker.SetJobStatus(ctx, j.JobID, "running", "")
		err = worker.ProcessJob(ctx, j, ropts)
		if err != nil {
			log.Printf("job %s failed: %v", j.JobID, err)
			_ = broker.SetJobStatus(ctx, j.JobID, "failed", err.Error())
			continue
		}
		_ = broker.SetJobStatus(ctx, j.JobID, "succeeded", "")
		log.Printf("job %s done", j.JobID)
	}
}

func runOnce(apiCfg *config.API, ropts chunk.RecursiveChunkOptions, inputPath, partition string, upsert, ensureCol, noFlush, dryRun, chunkExpand bool) {
	f, err := os.Open(inputPath)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	defer f.Close()

	if dryRun {
		inLines, outRows, err := pipeline.PreviewNDJSON(f, chunkExpand, ropts)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("dry-run: %d input lines -> %d chunk rows in %q (chunk=%v)", inLines, outRows, inputPath, chunkExpand)
		return
	}

	if !apiCfg.Milvus.MilvusEnabled() {
		log.Fatal("milvus.enabled 必须为 true")
	}
	mc := apiCfg.Milvus.ToMilvus()
	if err := mc.Validate(); err != nil {
		log.Fatalf("milvus config: %v", err)
	}

	emb, err := apiCfg.BuildEmbedder()
	if err != nil {
		log.Fatalf("embedding: %v", err)
	}
	if emb == nil {
		log.Fatal("请在配置中设置 embedding.enabled: true，并配置 http 或 local 后端")
	}

	logMilvusDialDebug(mc)

	ctx := context.Background()
	repo, err := milvus.NewRepository(ctx, mc)
	if err != nil {
		log.Fatalf("milvus connect: %v", err)
	}
	defer repo.Close()

	if ensureCol {
		if err := repo.EnsureCollection(ctx); err != nil {
			log.Fatalf("ensure collection: %v", err)
		}
		log.Printf("collection %q ready (db=%q addr=%q)", mc.Collection, mc.DBName, mc.Address)
	}

	var esRepo *es.Repository
	if apiCfg.Elasticsearch.ElasticsearchEnabled() {
		ec := apiCfg.Elasticsearch.ToElasticsearch()
		if err := ec.Validate(); err != nil {
			log.Fatalf("elasticsearch config: %v", err)
		}
		er, err := es.NewRepository(ec)
		if err != nil {
			log.Fatalf("elasticsearch client: %v", err)
		}
		if err := er.EnsureIndex(ctx); err != nil {
			log.Fatalf("elasticsearch ensure index: %v", err)
		}
		esRepo = er
		log.Printf("elasticsearch index %q ready (addr=%q)", ec.Index, ec.Addresses[0])
	}

	mb := apiCfg.Embedding.MaxBatch
	runner := &pipeline.Runner{Embedder: emb, Repo: repo, ES: esRepo, MaxBatch: mb}

	st, err := runner.RunNDJSON(ctx, f, pipeline.NDJSONRunOptions{
		Partition:   partition,
		Upsert:      upsert,
		ChunkExpand: chunkExpand,
		ChunkOpts:   ropts,
		Flush:       !noFlush,
	})
	if err != nil {
		log.Fatal(err)
	}

	if !noFlush {
		log.Print("flush done")
	}
	log.Printf("import finished: %d input lines -> %d chunks written from %q -> %s (chunk=%v)", st.InputLines, st.ChunksWritten, inputPath, mc.Collection, chunkExpand)
}

func logMilvusDialDebug(mc milvus.Config) {
	log.Printf("milvus dial: address=%q db=%q username=%q password=%q", mc.Address, mc.DBName, mc.Username, mc.Password)
}
