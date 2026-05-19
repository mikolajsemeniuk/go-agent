// Command seederv2 seeds the Milvus vector database with TechCorp's product
// documentation using the eino Milvus indexer. It embeds the markdown files
// under data/, chunks them, and stores one float-vector point per chunk.
//
// Prerequisites: Ollama running with the embedding model pulled, and the Milvus
// stack from docker-compose.yaml up.
package main

import (
	"context"
	"embed"
	"log"
	"time"

	"go-agent/pkg/agent"

	ollamaembed "github.com/cloudwego/eino-ext/components/embedding/ollama"
	"github.com/cloudwego/eino/schema"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

//go:embed data/*.md
var files embed.FS

func main() {
	ctx := context.Background()

	cfg := ollamaembed.EmbeddingConfig{
		BaseURL: agent.OllamaBaseURL,
		Model:   agent.EmbeddingModel,
		Timeout: 2 * time.Minute,
	}
	emb, err := agent.NewEmbedder(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}

	// Probe the embedding dimension so the Milvus schema matches the model.
	probe, err := emb.EmbedStrings(ctx, []string{"dimension probe"})
	if err != nil || len(probe) == 0 {
		log.Fatalf("cannot reach Ollama / embedding model %q: %v\nPull it with: ollama pull %s", agent.EmbeddingModel, err, agent.EmbeddingModel)
	}
	dim := len(probe[0])
	log.Printf("embedding dimension: %d", dim)

	milvus, err := agent.NewMilvusClient(ctx, client.Config{Address: agent.MilvusAddress})
	if err != nil {
		log.Fatalf("%v\nMake sure the Milvus stack is up: docker compose up -d", err)
	}
	defer milvus.Close()

	// Drop any existing collection so re-seeding is idempotent.
	exists, err := milvus.HasCollection(ctx, agent.MilvusCollection)
	if err != nil {
		log.Fatalf("milvus has collection: %v", err)
	}

	if exists {
		if err := milvus.DropCollection(ctx, agent.MilvusCollection); err != nil {
			log.Fatalf("milvus drop collection: %v", err)
		}
		log.Printf("dropped existing collection %q", agent.MilvusCollection)
	}

	indexer, err := agent.NewIndexer(ctx, milvus, emb, dim)
	if err != nil {
		log.Fatal(err)
	}

	entries, err := files.ReadDir("data")
	if err != nil || len(entries) == 0 {
		log.Fatalf("no .md files embedded under data/: %v", err)
	}

	var docs []*schema.Document
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		content, err := files.ReadFile("data/" + entry.Name())
		if err != nil {
			log.Printf("skipping %s: %v", entry.Name(), err)
			continue
		}

		chunks := agent.ChunkMarkdown(string(content), entry.Name())
		docs = append(docs, chunks...)
		log.Printf("[%s] %d chunks", entry.Name(), len(chunks))
	}

	if len(docs) == 0 {
		log.Fatal("no chunks produced from embedded data")
	}

	ids, err := indexer.Store(ctx, docs)
	if err != nil {
		log.Fatalf("store documents: %v", err)
	}

	log.Printf("seeded %d chunks into Milvus collection %q", len(ids), agent.MilvusCollection)
}
