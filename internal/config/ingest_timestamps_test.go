package config

import "testing"

func TestLoadIngestUseServerTimeFromEnv(t *testing.T) {
	t.Setenv("INGEST_USE_SERVER_TIME", "")
	if LoadIngestUseServerTimeFromEnv() {
		t.Fatal("want false when unset")
	}
	t.Setenv("INGEST_USE_SERVER_TIME", "true")
	if !LoadIngestUseServerTimeFromEnv() {
		t.Fatal("want true")
	}
	t.Setenv("INGEST_USE_SERVER_TIME", "1")
	if !LoadIngestUseServerTimeFromEnv() {
		t.Fatal("want true for 1")
	}
}
