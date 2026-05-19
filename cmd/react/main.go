// Command react runs the TechCorp customer-update flow as an eino ReAct agent.
// The agent is given the MCP tools (search_docs, list_customers, find_customer,
// update_customer) and is instructed to reason through the same four steps the
// graph performs explicitly — gather the request, retrieve documentation,
// inspect the record, and update it — but here the loop is driven by the model.
//
// Unlike cmd/graph (a single finite pipeline), this is an interactive chat
// loop: it keeps prompting and answering, preserving conversation history,
// until the user types "exit".
//
// Prerequisites: Ollama running with both models, the Milvus stack up, and the
// collection seeded (go run ./cmd/seederv2).
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"go-agent/pkg/agent"

	ollamaembed "github.com/cloudwego/eino-ext/components/embedding/ollama"
	ollamachat "github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
)

const systemPrompt = `You are TechCorp's customer-operations agent. Handle the
user's request by following exactly these four steps, in order:

1. Understand the user's request.
2. Call search_docs to pull the relevant TechCorp product documentation.
3. Call find_customer (or list_customers) to inspect the current customer record.
4. Call update_customer to apply the change, using what you learned from the
   documentation and the current record.

Only change the fields the user actually asked about. After the update, reply
with a short confirmation that states the old and new values.`

func main() {
	ctx := context.Background()

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
	})
	if err != nil {
		log.Fatal(err)
	}

	// MCP server exposes the customer tools; AddSearchTool adds RAG retrieval
	// as a fourth tool, so the agent reaches everything over MCP.
	server := agent.NewMCPServer()
	agent.AddSearchTool(server, retriever)

	client, err := agent.NewMCPClient(ctx, server)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	tools, err := agent.MCPTools(ctx, client)
	if err != nil {
		log.Fatal(err)
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: model,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		MaxStep:          24,
	})
	if err != nil {
		log.Fatalf("build react agent: %v", err)
	}

	history := []*schema.Message{schema.SystemMessage(systemPrompt)}
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("TechCorp agent ready. Type a request, or 'exit' to quit.")
	for {
		fmt.Print("\n> ")
		line, err := reader.ReadString('\n')
		if err == io.EOF { // Ctrl+D
			fmt.Println()
			return
		}

		if err != nil {
			log.Fatalf("read input: %v", err)
		}

		request := strings.TrimSpace(line)
		if strings.EqualFold(request, "exit") {
			fmt.Println("bye")
			return
		}

		history = append(history, schema.UserMessage(request))
		out, err := agent.Generate(ctx, history)
		if err != nil {
			log.Printf("react agent: %v", err)
			history = history[:len(history)-1] // drop the unanswered turn
			continue
		}

		fmt.Printf("\n%s\n", out.Content)
		history = append(history, out)
	}
}
