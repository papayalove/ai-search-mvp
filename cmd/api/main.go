package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"ai-search-v1/internal/app"
	"ai-search-v1/internal/config"
	querypipe "ai-search-v1/internal/query/pipeline"
	"ai-search-v1/internal/queue"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"
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
		searcher = querypipe.NewMilvusSearcher(repo, emb)
		log.Print("searcher: MilvusSearcher (POST /v1/admin/query chunk lookup; POST /v1/search vector ANN same as admin text)")
		if emb == nil {
			log.Print("embedding disabled: text chunk lookup and vector search need embedding.enabled: true")
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
			log.Printf("elasticsearch index %q ready (addr=%q)", ec.Index, ec.Addresses[0])
		}
	} else {
		log.Print("milvus disabled in config (milvus.enabled: false)")
	}

	srv := app.NewHTTPServer(searcher, ingestBroker, qe, chOpts, milvusRepo)
	log.Printf("api listening on %s (config %q)", addr, cfgPath)
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatal(err)
	}
}
