package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcptool "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type Customer struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
	Email   string `json:"email"`
}

var (
	customersMu sync.Mutex
	customers   = []Customer{
		{ID: 1, Name: "Alice", Country: "PL", Email: "alice@example.com"},
		{ID: 2, Name: "Bob", Country: "DE", Email: "bob@example.com"},
		{ID: 3, Name: "Carol", Country: "US", Email: "carol@example.com"},
	}
)

func NewMCPServer() *server.MCPServer {
	s := server.NewMCPServer("techcorp-agentv2", "1.0.0", server.WithToolCapabilities(true))

	s.AddTool(
		mcp.NewTool("list_customers",
			mcp.WithDescription("Lists every customer in the database (id, name, country, email).")),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			customersMu.Lock()
			defer customersMu.Unlock()

			snapshot := make([]Customer, len(customers))
			copy(snapshot, customers)

			return jsonResult(map[string]any{"customers": snapshot})
		},
	)

	s.AddTool(
		mcp.NewTool("find_customer",
			mcp.WithDescription("Finds a single customer by id and returns their record. Errors if no such customer exists."),
			mcp.WithNumber("id",
				mcp.Description("the customer id to look up"),
				mcp.Required())),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			customersMu.Lock()
			defer customersMu.Unlock()
			for _, c := range customers {
				if c.ID == id {
					return jsonResult(c)
				}
			}

			return mcp.NewToolResultError(fmt.Sprintf("no customer with id=%d", id)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("update_customer",
			mcp.WithDescription("Updates an existing customer in the database. Provide the customer's id plus any of: name, country, email. Empty or omitted fields are left unchanged. Returns the customer after the update."),
			mcp.WithNumber("id",
				mcp.Description("the customer id to update"),
				mcp.Required()),
			mcp.WithString("name",
				mcp.Description("new name; leave empty to keep the current one")),
			mcp.WithString("country",
				mcp.Description("new country code; leave empty to keep the current one")),
			mcp.WithString("email",
				mcp.Description("new email; leave empty to keep the current one"))),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			name := req.GetString("name", "")
			country := req.GetString("country", "")
			email := req.GetString("email", "")

			customersMu.Lock()
			defer customersMu.Unlock()
			for i := range customers {
				if customers[i].ID != id {
					continue
				}

				if name != "" {
					customers[i].Name = name
				}

				if country != "" {
					customers[i].Country = country
				}

				if email != "" {
					customers[i].Email = email
				}

				return jsonResult(customers[i])
			}

			return mcp.NewToolResultError(fmt.Sprintf("no customer with id=%d", id)), nil
		},
	)

	return s
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(b)), nil
}

func NewMCPClient(ctx context.Context, s *server.MCPServer) (*client.Client, error) {
	client, err := client.NewInProcessClient(s)
	if err != nil {
		return nil, fmt.Errorf("mcp client: %w", err)
	}

	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("mcp client start: %w", err)
	}

	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "techcorp-agentv2", Version: "1.0.0"}
	if _, err := client.Initialize(ctx, req); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	return client, nil
}

func MCPTools(ctx context.Context, cli client.MCPClient) ([]tool.BaseTool, error) {
	tools, err := mcptool.GetTools(ctx, &mcptool.Config{Cli: cli})
	if err != nil {
		return nil, fmt.Errorf("mcp get tools: %w", err)
	}

	return tools, nil
}

func CallTool(ctx context.Context, cli client.MCPClient, name string, args map[string]any) (string, error) {
	res, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		return "", fmt.Errorf("call %s: %w", name, err)
	}

	text := resultText(res)
	if res.IsError {
		return "", fmt.Errorf("tool %s failed: %s", name, text)
	}

	return text, nil
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}

	return b.String()
}
