// Importer：-input 时单次 NDJSON 文件导入；无 -input 时作为队列 Worker 消费 Redis。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"ai-search-v1/internal/config"
	"ai-search-v1/internal/ingest/chunk"
	ingestmeta "ai-search-v1/internal/ingest/meta"
	"ai-search-v1/internal/ingest/pipeline"
	"ai-search-v1/internal/queue"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"
	"ai-search-v1/internal/storage/mysqldb"
	storages3 "ai-search-v1/internal/storage/s3"
)

// stderrNow 直接写 stderr 并尽量刷盘；部分 Windows/Cursor 终端对 log 包或缓冲不友好时仍能看到这一行。
func stderrNow(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
	if !strings.HasSuffix(format, "\n") {
		_, _ = fmt.Fprintln(os.Stderr)
	}
	_ = os.Stderr.Sync()
}

// resolveImporterHTTPAddr Worker HTTP 监听地址。
// - 未设置环境变量：默认 ":18080"（与 API 的 HTTP_ADDR 区分）
// - IMPORTER_HTTP_ADDR= 显式为空：不启用 HTTP
// - 其它：按原样使用（如 "127.0.0.1:18080"）
func resolveImporterHTTPAddr() string {
	v, ok := os.LookupEnv("IMPORTER_HTTP_ADDR")
	if !ok {
		return ":18080"
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return v
}

func startImporterHTTPServer(ctx context.Context, addr, redisListKey string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		body, err := json.Marshal(map[string]string{
			"role":       "importer_worker",
			"redis_list": redisListKey,
		})
		if err != nil {
			http.Error(w, "encode", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append(body, '\n'))
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("importer http: listening on %s (GET /health, GET /)", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("importer http: ListenAndServe: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("importer http: Shutdown: %v", err)
		}
	}()
}

// importerRequireSingletonLock 为 true 时持 Redis 单例锁，禁止同队列多进程；默认 false 允许多 worker 并行消费。
func importerRequireSingletonLock(cmdFlag bool) bool {
	if cmdFlag {
		return true
	}
	v := strings.TrimSpace(os.Getenv("IMPORTER_REQUIRE_SINGLETON_LOCK"))
	return strings.EqualFold(v, "true") || v == "1"
}

func main() {
	config.LoadDotEnv()

	configPath := flag.String("config", "", "api.yaml 路径（默认 configs/api.yaml）")
	inputPath := flag.String("input", "", "NDJSON 输入文件；留空则启动队列 Worker（需 REDIS_INGEST_URL 或 REDIS_INGEST_HOST）")
	partition := flag.String("partition", "", "Milvus 分区名（可选，仅 -input 模式）")
	upsert := flag.Bool("upsert", true, "使用 Upsert 代替 Insert（默认 true；传 -upsert=false 则 Insert）")
	ensure := flag.Bool("ensure-collection", true, "若集合不存在则创建、建索引并 Load")
	noFlush := flag.Bool("no-flush", false, "导入结束后不执行 Flush（仅 -input）")
	dryRun := flag.Bool("dry-run", false, "只解析输入并统计行数（需 -input）")
	chunkSizeF := flag.Int("chunk-size", -1, "覆盖 ingest.chunk_size，-1 使用配置文件")
	chunkOverlapF := flag.Int("chunk-overlap", -1, "覆盖 ingest.chunk_overlap，-1 使用配置文件")
	ingestWorkers := flag.Int("workers", -1, "本进程内并发消费协程数；-1 使用 REDIS_INGEST_WORKER_CONCURRENCY（默认 1）")
	requireSingletonLock := flag.Bool("require-singleton-lock", false, "持 Redis 单例锁，同队列仅允许一个 importer 进程（也可用 IMPORTER_REQUIRE_SINGLETON_LOCK=true）")
	flag.Parse()

	def := config.DefaultAPIConfigPath
	if *configPath == "" {
		*configPath = def
	}

	// Worker 模式在 LoadAPI 之前就打一行，避免「读 yaml 卡住时终端像死了一样」。
	if *inputPath == "" && !*dryRun {
		stderrNow("[cmd/importer] worker mode: loading config %q ...\n", *configPath)
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
		stderrNow("[cmd/importer] config OK, entering runWorker (Milvus/Redis/MySQL setup next)\n")
		runWorker(apiCfg, ropts, *ensure, *ingestWorkers, importerRequireSingletonLock(*requireSingletonLock))
		return
	}

	runOnce(apiCfg, ropts, *inputPath, *partition, *upsert, *ensure, *noFlush, *dryRun)
}

func runWorker(apiCfg *config.API, ropts chunk.RecursiveChunkOptions, ensureCol bool, workersOverride int, requireSingletonLock bool) {
	wd, _ := os.Getwd()
	stderrNow("[cmd/importer] runWorker start cwd=%s\n", wd)
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("importer worker: 启动中 cwd=%q（日志在 stderr；.env 从含 go.mod 的模块根加载）", wd)
	_ = os.Stderr.Sync()

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

	// Windows 上 Ctrl+C 应对应 os.Interrupt；仅 syscall.SIGINT 在部分终端下收不到，导致 BRPop 永久阻塞无法退出。
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := broker.Ping(ctx); err != nil {
		log.Fatalf("redis ping: %v", err)
	}

	if requireSingletonLock {
		releaseLock, err := queue.HoldImporterSingletonLock(ctx, broker.Client, qe.QueueListKey)
		if err != nil {
			log.Fatalf("importer singleton lock: %v", err)
		}
		defer releaseLock()
		log.Printf("importer: singleton lock acquired %q (second cmd/importer for this queue will exit)", queue.ImporterSingletonLockKey(qe.QueueListKey))
	} else {
		log.Print("importer: no singleton lock — multiple processes may consume the same queue; ProcessJob runs in parallel across goroutines")
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

	im := config.LoadIngestMetaFromEnv()
	dsnConfigured := strings.TrimSpace(im.MySQLDSN) != ""
	log.Printf("importer: MYSQL_DSN 在环境中已配置=%v INGEST_META_ENABLED(仅影响 API 入队写元数据)=%v", dsnConfigured, im.Enabled)
	if !dsnConfigured {
		log.Print("importer: WARNING — 未配置 MYSQL_DSN，本 Worker 不会更新 ingest_job（无 MarkRunning/MarkTerminal，亦无 ingest_job: 日志）；Milvus/ES 仍会按队列写入。请把 MYSQL_DSN 写在仓库根目录 .env，并从能解析到该 go.mod 的目录执行 go run ./cmd/importer。")
	}
	if err := mysqldb.Init(im.MySQLDSN); err != nil {
		log.Fatalf("mysql init: %v", err)
	}
	defer func() { _ = mysqldb.CloseGlobal() }()
	if mysqldb.Repo() != nil {
		log.Print("mysql: global pool initialized (MYSQL_DSN)")
	}

	// API 在 INGEST_META_ENABLED 时写入 ingest_job；Worker 只要连上同一 MySQL 就应更新状态，
	// 否则会出现行一直停留在 queued（此前仅在 INGEST_META_ENABLED 时注入 Meta，Worker 易漏配）。
	var ingestMeta *ingestmeta.Service
	if mysqldb.Repo() != nil {
		var taskIdx *es.IngestTaskIndex
		if esRepo != nil {
			taskIdx = es.NewIngestTaskIndex(esRepo, im.TaskESIndex)
			if err := taskIdx.EnsureIndex(ctx); err != nil {
				log.Fatalf("ingest meta elasticsearch task index: %v", err)
			}
			log.Printf("ingest meta: elasticsearch task index %q ready (worker)", im.TaskESIndex)
		} else {
			log.Print("ingest meta: elasticsearch disabled on worker; MySQL ingest_job updates only (no ES task docs)")
		}
		ingestMeta = ingestmeta.NewService(mysqldb.Repo(), taskIdx)
		if im.Enabled {
			log.Printf("ingest meta: full mode (INGEST_ES_TASK_INDEX=%q)", im.TaskESIndex)
		} else {
			log.Print("ingest meta: worker MySQL job lifecycle enabled (set INGEST_META_ENABLED=true on API to create rows + ES tasks on enqueue)")
		}
	}

	runner := &pipeline.Runner{
		Embedder: emb,
		Repo:     repo,
		ES:       esRepo,
		MaxBatch: apiCfg.Embedding.MaxBatch,
	}
	worker := &pipeline.JobWorker{Runner: runner, Broker: broker, S3: s3cli, Meta: ingestMeta}
	// 非环境变量：本进程是否会把出队任务写回 MySQL ingest_job（仅当 MYSQL_DSN 已加载且 Repo 非空）。
	// INGEST_META_ENABLED 只控制「API 入队时是否 INSERT 任务行」；Worker 侧更新行不读该开关，只读 MYSQL_DSN。
	willUpdateIngestJobTable := ingestMeta != nil && ingestMeta.Enabled()
	if willUpdateIngestJobTable {
		log.Print("importer: 将更新 MySQL ingest_job 表（MarkRunning / MarkTerminal）；依赖本进程已加载 MYSQL_DSN")
	} else {
		log.Print("importer: 不会更新 MySQL ingest_job 表（本进程未连接 MySQL）；INGEST_META_ENABLED 不能替代 MYSQL_DSN")
	}

	if httpAddr := resolveImporterHTTPAddr(); httpAddr != "" {
		startImporterHTTPServer(ctx, httpAddr, qe.QueueListKey)
	} else {
		log.Print("importer http: disabled (IMPORTER_HTTP_ADDR is empty)")
	}

	n := qe.WorkerConcurrency
	if workersOverride >= 1 {
		n = workersOverride
	}
	if n < 1 {
		n = 1
	}
	const maxIngestWorkerGoroutines = 32
	if n > maxIngestWorkerGoroutines {
		log.Fatalf("workers=%d exceeds max %d", n, maxIngestWorkerGoroutines)
	}
	log.Printf("importer: consumer goroutines=%d (parallel dequeue and parallel ProcessJob when n>1)", n)

	// 使用有限超时 BRPop，周期性醒来以检查 ctx（信号取消）；timeout=0 在 Windows 上易与 Ctrl+C 冲突。
	const dequeueWait = 5 * time.Second
	const idleHeartbeatEvery = 60 * time.Second
	log.Printf("importer worker listening on redis list %q (dequeue idle poll=%s)", qe.QueueListKey, dequeueWait)
	log.Printf("importer: 队列为空时不会打印「入库」日志；有任务入队后才会出现 dequeued / ingest_job: / job done。若长期无任务，每 %s 打一行空闲心跳。", idleHeartbeatEvery)

	var idleMu sync.Mutex
	var lastIdleHeartbeat time.Time
	markQueueActive := func() {
		idleMu.Lock()
		lastIdleHeartbeat = time.Time{}
		idleMu.Unlock()
	}
	maybeIdleHeartbeat := func() {
		idleMu.Lock()
		defer idleMu.Unlock()
		now := time.Now()
		if lastIdleHeartbeat.IsZero() || now.Sub(lastIdleHeartbeat) >= idleHeartbeatEvery {
			log.Printf("importer: heartbeat — queue idle, waiting on %q (enqueue with POST /v1/admin/ingest or /v1/admin/ingest/remote; same REDIS_INGEST_* as this worker)", qe.QueueListKey)
			lastIdleHeartbeat = now
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		workerID := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := broker.Dequeue(ctx, dequeueWait)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					if errors.Is(err, queue.ErrDequeueIdle) {
						maybeIdleHeartbeat()
						continue
					}
					log.Printf("dequeue error: %v", err)
					continue
				}
				markQueueActive()
				log.Printf("importer: worker %d dequeued job_id=%s payload_kind=%s will_update_mysql_ingest_job=%v", workerID, j.JobID, j.PayloadKind, willUpdateIngestJobTable)
				_ = broker.SetJobStatus(ctx, j.JobID, "running", "")
				err = worker.ProcessJob(ctx, j, ropts)
				if err != nil {
					log.Printf("job %s failed: %v", j.JobID, err)
					_ = broker.SetJobStatus(ctx, j.JobID, "failed", err.Error())
					continue
				}
				_ = broker.SetJobStatus(ctx, j.JobID, "succeeded", "")
				log.Printf("job %s done (worker %d)", j.JobID, workerID)
			}
		}()
	}
	go func() {
		<-ctx.Done()
		log.Print("importer worker: shutdown (signal or context cancel)")
	}()
	wg.Wait()
}

func runOnce(apiCfg *config.API, ropts chunk.RecursiveChunkOptions, inputPath, partition string, upsert, ensureCol, noFlush, dryRun bool) {
	f, err := os.Open(inputPath)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	defer f.Close()

	if dryRun {
		inLines, outRows, err := pipeline.PreviewNDJSON(f, ropts, filepath.Ext(inputPath), "")
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("dry-run: %d input lines -> %d chunk rows in %q", inLines, outRows, inputPath)
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
		Partition:     partition,
		Upsert:        upsert,
		ChunkOpts:     ropts,
		Flush:         !noFlush,
		FileExt:       filepath.Ext(inputPath),
		JobSourceType: "",
	})
	if err != nil {
		log.Fatal(err)
	}

	if !noFlush {
		log.Print("flush done")
	}
	log.Printf("import finished: %d input lines -> %d chunks written from %q -> %s", st.InputLines, st.ChunksWritten, inputPath, mc.Collection)
}

func logMilvusDialDebug(mc milvus.Config) {
	log.Printf("milvus dial: address=%q db=%q username=%q password=%q", mc.Address, mc.DBName, mc.Username, mc.Password)
}
