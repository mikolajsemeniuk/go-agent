package agent

import (
	"context"
	"encoding/json"
	"fmt"

	milvusidx "github.com/cloudwego/eino-ext/components/indexer/milvus"
	milvusret "github.com/cloudwego/eino-ext/components/retriever/milvus"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const (
	MilvusAddress    = "localhost:19530"
	MilvusCollection = "techcorp_docs"
	DefaultTopK      = 4
)

func NewMilvusClient(ctx context.Context, cfg client.Config) (client.Client, error) {
	client, err := client.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("milvus connect: %w", err)
	}

	return client, nil
}

func milvusFields(dim int) []*entity.Field {
	return []*entity.Field{
		entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).
			WithIsPrimaryKey(true).WithMaxLength(256),
		entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).
			WithDim(int64(dim)),
		entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).
			WithMaxLength(8192),
		entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON),
	}
}

type milvusRow struct {
	ID       string    `json:"id"       milvus:"name:id"`
	Vector   []float32 `json:"vector"   milvus:"name:vector"`
	Content  string    `json:"content"  milvus:"name:content"`
	Metadata []byte    `json:"metadata" milvus:"name:metadata"`
}

func NewIndexer(ctx context.Context, cli client.Client, emb embedding.Embedder, dim int) (*milvusidx.Indexer, error) {
	converter := func(_ context.Context, docs []*schema.Document, vectors [][]float64) ([]any, error) {
		if len(vectors) != len(docs) {
			return nil, fmt.Errorf("vectors/docs length mismatch: %d vs %d", len(vectors), len(docs))
		}

		rows := make([]any, 0, len(docs))
		for i, doc := range docs {
			meta, err := json.Marshal(doc.MetaData)
			if err != nil {
				return nil, fmt.Errorf("marshal metadata: %w", err)
			}

			vec := make([]float32, len(vectors[i]))
			for j, v := range vectors[i] {
				vec[j] = float32(v)
			}

			row := &milvusRow{
				ID:       doc.ID,
				Vector:   vec,
				Content:  doc.Content,
				Metadata: meta,
			}
			rows = append(rows, row)
		}
		return rows, nil
	}

	cfg := &milvusidx.IndexerConfig{
		Client:            cli,
		Collection:        MilvusCollection,
		Fields:            milvusFields(dim),
		MetricType:        milvusidx.COSINE,
		Embedding:         emb,
		DocumentConverter: converter,
	}
	indexer, err := milvusidx.NewIndexer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("milvus indexer: %w", err)
	}

	return indexer, nil
}

func NewRetriever(ctx context.Context, cli client.Client, emb embedding.Embedder) (*milvusret.Retriever, error) {
	params, err := entity.NewIndexAUTOINDEXSearchParam(1)
	if err != nil {
		return nil, fmt.Errorf("milvus search param: %w", err)
	}

	converter := func(_ context.Context, vectors [][]float64) ([]entity.Vector, error) {
		out := make([]entity.Vector, 0, len(vectors))
		for _, v := range vectors {
			fv := make(entity.FloatVector, len(v))
			for i, f := range v {
				fv[i] = float32(f)
			}

			out = append(out, fv)
		}

		return out, nil
	}

	cfg := &milvusret.RetrieverConfig{
		Client:          cli,
		Collection:      MilvusCollection,
		VectorField:     "vector",
		OutputFields:    []string{"content", "metadata"},
		MetricType:      entity.COSINE,
		TopK:            DefaultTopK,
		Sp:              params,
		Embedding:       emb,
		VectorConverter: converter,
	}
	retriever, err := milvusret.NewRetriever(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("milvus retriever: %w", err)
	}

	return retriever, nil
}

func AddSearchTool(s *server.MCPServer, rtr *milvusret.Retriever) {
	tool := mcp.NewTool("search_docs",
		mcp.WithDescription("Searches TechCorp's product documentation and returns the most relevant fragments for a natural-language query."),
		mcp.WithString("query",
			mcp.Description("the natural-language search query"),
			mcp.Required()),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		docs, err := rtr.Retrieve(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("retrieve: %v", err)), nil
		}
		return mcp.NewToolResultText(FormatDocs(docs)), nil
	})
}
