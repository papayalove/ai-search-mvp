package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"ai-search-v1/internal/app"
	"ai-search-v1/internal/config"
	ingestmeta "ai-search-v1/internal/ingest/meta"
	querypipe "ai-search-v1/internal/query/pipeline"
	"ai-search-v1/internal/queue"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"
	"ai-search-v1/internal/storage/mysqldb"
)

func main() {
	config.LoadDotEnv()
	cfgPath := config.ResolveAPIConfigPath(config.DefaultAPIConfigPath)
	apiCfg, err := config.LoadAPI(cfgPath)
	if err != nil {
		log.Fatalf("load api config %q: %v", cfgPath, err)
	}

	addr := apiCfg.HTTP.Addr
	if e := os.Getenv("HTTP_ADDR"); strings.TrimSpace(e) != "" {
		addr = e
	}

	ctx := context.Background()

	im := config.LoadIngestMetaFromEnv()
	if err := mysqldb.Init(im.MySQLDSN); err != nil {
		log.Fatalf("mysql init: %v", err)
	}
	defer func() { _ = mysqldb.CloseGlobal() }()
	if mysqldb.Repo() != nil {
		log.Print("mysql: global pool initialized (MYSQL_DSN)")
	}

	chOpts := apiCfg.Ingest.ToRecursiveChunkOptions()
	qe := config.LoadIngestQueueFromEnv()
	var ingestBroker *queue.RedisBroker
	if qe.Enabled && qe.RedisURL != "" {
		b, err := queue.NewRedisBroker(qe.RedisURL, qe.QueueListKey)
		if err != nil {
			log.Fatalf("ingest redis: %v", err)
		}
		if err := b.Ping(ctx); err != nil {
			log.Fatalf("ingest redis ping: %v", err)
		}
		ingestBroker = b
		log.Printf("ingest queue: redis list %q (multipart payload TTL=%v, remote job meta TTL=%v)", qe.QueueListKey, qe.MultipartPayloadTTL, qe.RemoteJobMetaTTL)
	} else {
		log.Print("ingest queue: disabled (set REDIS_INGEST_URL or REDIS_INGEST_HOST and REDIS_INGEST_ENABLED=true)")
	}

	var milvusRepo *milvus.Repository
	var esRepo *es.Repository
	var milvusSearcher *querypipe.MilvusSearcher
	searcher := querypipe.Searcher(querypipe.StubSearcher{})
	if apiCfg.Milvus.MilvusEnabled() {
		mc := apiCfg.Milvus.ToMilvus()
		repo, err := milvus.Get(ctx, mc)
		if err != nil {
			log.Fatalf("milvus connect: %v", err)
		}
		milvusRepo = repo
		log.Printf("milvus connected (%s collection=%s dim=%d)", mc.Address, mc.Collection, mc.VectorDim)
		if err := repo.EnsureCollection(ctx); err != nil {
			log.Fatalf("milvus ensure collection: %v", err)
		}
		emb, err := apiCfg.BuildEmbedder()
		if err != nil {
			log.Fatalf("embedding: %v", err)
		}
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
		milvusSearcher = querypipe.NewMilvusSearcher(repo, emb, esRepo)
		searcher = milvusSearcher
		milvusSearcher.Rewriter = config.LoadRewriterFromEnv()
		log.Print("searcher: MilvusSearcher (hybrid text search when ES enabled; POST /v1/search retrieval=hybrid|milvus|es)")
		if emb == nil {
			log.Print("embedding disabled: milvus-only text modes need embedding.enabled: true")
		}
	} else {
		log.Print("milvus disabled in config (milvus.enabled: false)")
	}

	var ingestMeta *ingestmeta.Service
	if im.Enabled {
		if mysqldb.Repo() == nil {
			log.Fatal("ingest meta enabled but MYSQL_DSN is empty or mysql init failed")
		}
		var taskIdx *es.IngestTaskIndex
		if esRepo != nil {
			taskIdx = es.NewIngestTaskIndex(esRepo, im.TaskESIndex)
			if err := taskIdx.EnsureIndex(ctx); err != nil {
				log.Fatalf("ingest meta elasticsearch task index: %v", err)
			}
			log.Printf("ingest meta: elasticsearch task index %q ready", im.TaskESIndex)
		} else {
			log.Print("ingest meta: elasticsearch disabled; task documents will not be written (MySQL job rows only)")
		}
		ingestMeta = ingestmeta.NewService(mysqldb.Repo(), taskIdx)
		log.Printf("ingest meta: mysql ingest_job enabled (INGEST_ES_TASK_INDEX=%q)", im.TaskESIndex)
	}

	srv := app.NewHTTPServer(searcher, ingestBroker, qe, chOpts, milvusRepo, ingestMeta)
	log.Printf("api listening on %s (config %q)", addr, cfgPath)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatal(err)
	}
}
