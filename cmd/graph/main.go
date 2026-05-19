// Command graph runs the TechCorp customer-update pipeline as a deterministic
// eino compose.Graph. The graph has exactly four nodes, one per required step:
//
//  1. collect_input — gather the request (customer id + instruction) from the user
//  2. rag_retrieve  — pull relevant documentation from the Milvus RAG store
//  3. mcp_inspect   — read the current customer record through the MCP server
//  4. mcp_modify    — let the chat model decide the new values, then write them
//     back through the MCP server's update_customer tool
//
// Prerequisites: Ollama running with both models, the Milvus stack up, and the
// collection seeded (go run ./cmd/seederv2).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"go-agent/pkg/agent"

	ollamaembed "github.com/cloudwego/eino-ext/components/embedding/ollama"
	ollamachat "github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

// exchange is the value threaded through the graph nodes. Each node enriches it
// and passes it on, so the edges form a single typed data flow.
type exchange struct {
	CustomerID  int    // customer to update
	Instruction string // what the user asked for
	RAGContext  string // documentation fragments from the RAG store
	Customer    string // current customer record (JSON) from the MCP server
}

// updatePlanSchema is the JSON Schema Ollama enforces (via the `format` field)
// on the chat model's reply in node 4. Constrained decoding then guarantees the
// reply parses into the plan struct: exactly these three string fields, no
// extra keys.
const updatePlanSchema = `{
  "type": "object",
  "properties": {
    "name":    {"type": "string"},
    "country": {"type": "string"},
    "email":   {"type": "string"}
  },
  "required": ["name", "country", "email"],
  "additionalProperties": false
}`

func main() {
	ctx := context.Background()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Customer ID: ")
	idLine, _ := reader.ReadString('\n')
	fmt.Print("What should change (you may reference TechCorp products): ")
	instrLine, _ := reader.ReadString('\n')
	idLine, instrLine = strings.TrimSpace(idLine), strings.TrimSpace(instrLine)
	if idLine == "" || instrLine == "" {
		log.Fatal("both a customer id and an instruction are required")
	}

	emb, err := agent.NewEmbedder(ctx, ollamaembed.EmbeddingConfig{
		BaseURL: agent.OllamaBaseURL,
		Model:   agent.EmbeddingModel,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		log.Fatal(err)
	}

	milvus, err := agent.NewMilvusClient(ctx, client.Config{Address: agent.MilvusAddress})
	if err != nil {
		log.Fatalf("%v\nMake sure Milvus is up and seeded (go run ./cmd/seeder)", err)
	}
	defer milvus.Close()

	retriever, err := agent.NewRetriever(ctx, milvus, emb)
	if err != nil {
		log.Fatal(err)
	}

	model, err := agent.NewChatModel(ctx, ollamachat.ChatModelConfig{
		BaseURL: agent.OllamaBaseURL,
		Model:   agent.ChatModel,
		Timeout: 5 * time.Minute,
		Format:  json.RawMessage(updatePlanSchema),
	})
	if err != nil {
		log.Fatal(err)
	}

	mcpServer := agent.NewMCPServer()
	mcpCli, err := agent.NewMCPClient(ctx, mcpServer)
	if err != nil {
		log.Fatal(err)
	}
	defer mcpCli.Close()

	collectInput := func(_ context.Context, raw string) (*exchange, error) {
		parts := strings.SplitN(strings.TrimSpace(raw), "\n", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("expected %q, got %q", "<id>\\n<instruction>", raw)
		}

		id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid customer id %q: %w", parts[0], err)
		}

		ex := &exchange{CustomerID: id, Instruction: strings.TrimSpace(parts[1])}
		log.Printf("step 1/4 collect_input — customer #%d: %s", ex.CustomerID, ex.Instruction)
		return ex, nil
	}

	ragRetrieve := func(ctx context.Context, ex *exchange) (*exchange, error) {
		docs, err := retriever.Retrieve(ctx, ex.Instruction)
		if err != nil {
			return nil, fmt.Errorf("rag retrieve: %w", err)
		}

		ex.RAGContext = agent.FormatDocs(docs)
		log.Printf("step 2/4 rag_retrieve — %d documentation fragment(s)", len(docs))
		return ex, nil
	}

	mcpInspect := func(ctx context.Context, ex *exchange) (*exchange, error) {
		current, err := agent.CallTool(ctx, mcpCli, "find_customer",
			map[string]any{"id": ex.CustomerID})
		if err != nil {
			return nil, err
		}

		ex.Customer = current
		log.Printf("step 3/4 mcp_inspect — current record: %s", current)
		return ex, nil
	}

	mcpModify := func(ctx context.Context, ex *exchange) (string, error) {
		prompt := fmt.Sprintf(`Current customer record (JSON):
%s

Relevant TechCorp documentation:
%s

User request: %s

Decide the new field values for this customer. Reply with ONLY a JSON object
with the keys "name", "country" and "email". Use an empty string for any field
that must stay unchanged.`, ex.Customer, ex.RAGContext, ex.Instruction)

		msg, err := model.Generate(ctx, []*schema.Message{
			schema.SystemMessage("You are a precise data-entry assistant. Output a JSON object and nothing else."),
			schema.UserMessage(prompt),
		})
		if err != nil {
			return "", fmt.Errorf("chat model: %w", err)
		}

		var plan struct {
			Name    string `json:"name"`
			Country string `json:"country"`
			Email   string `json:"email"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &plan); err != nil {
			return "", fmt.Errorf("parse update plan %q: %w", msg.Content, err)
		}

		args := map[string]any{"id": ex.CustomerID}
		if plan.Name != "" {
			args["name"] = plan.Name
		}
		if plan.Country != "" {
			args["country"] = plan.Country
		}
		if plan.Email != "" {
			args["email"] = plan.Email
		}

		updated, err := agent.CallTool(ctx, mcpCli, "update_customer", args)
		if err != nil {
			return "", err
		}

		log.Printf("step 4/4 mcp_modify — update applied")
		return fmt.Sprintf("Updated customer record: %s", updated), nil
	}

	g := compose.NewGraph[string, string]()
	errs := []error{
		g.AddLambdaNode("collect_input", compose.InvokableLambda(collectInput)),
		g.AddLambdaNode("rag_retrieve", compose.InvokableLambda(ragRetrieve)),
		g.AddLambdaNode("mcp_inspect", compose.InvokableLambda(mcpInspect)),
		g.AddLambdaNode("mcp_modify", compose.InvokableLambda(mcpModify)),
		g.AddEdge(compose.START, "collect_input"),
		g.AddEdge("collect_input", "rag_retrieve"),
		g.AddEdge("rag_retrieve", "mcp_inspect"),
		g.AddEdge("mcp_inspect", "mcp_modify"),
		g.AddEdge("mcp_modify", compose.END),
	}
	for _, err := range errs {
		if err != nil {
			log.Fatalf("build graph: %v", err)
		}
	}

	run, err := g.Compile(ctx)
	if err != nil {
		log.Fatalf("compile graph: %v", err)
	}

	out, err := run.Invoke(ctx, idLine+"\n"+instrLine)
	if err != nil {
		log.Fatalf("graph: %v", err)
	}

	fmt.Println()
	fmt.Println(out)
}
