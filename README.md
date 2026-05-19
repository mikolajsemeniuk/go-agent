# go-agent

## Setup models

```sh
ollama pull qwen3.6:35b-a3b-coding-nvfp4   # chat model (~21 GB)
ollama pull nomic-embed-text:latest        # embedding model
```

## Setup Milvus

```sh
# 1. Start the Milvus stack (etcd + minio + milvus). Wait ~90s until healthy.
docker compose up -d
docker compose ps          # milvus should report (healthy)

# 2. Seed the Milvus vector DB from cmd/seederv2/data/*.md
go run ./cmd/seeder
```

Expected seeder output:

```
embedding dimension: 768
[products.md] 3 chunks
seeded 3 chunks into Milvus collection "techcorp_docs"
```

Re-running the seeder is safe — it drops and recreates the collection.

## cmd/graph — deterministic 4-node pipeline

`compose.Graph` with one node per step. The chat model is called inside node 4
to turn the request + documentation + current record into the new field values.

```sh
go run ./cmd/graph
```

### Example: a plain change

```
Customer ID: 2
What should change (you may reference TechCorp products): change name to Mike
2026/05/18 22:46:20 step 1/4 collect_input — customer #2: change name to Mike
2026/05/18 22:46:20 step 2/4 rag_retrieve — 3 documentation fragment(s)
2026/05/18 22:46:20 step 3/4 mcp_inspect — current record: {"id":2,"name":"Bob","country":"DE","email":"bob@example.com"}
2026/05/18 22:47:05 step 4/4 mcp_modify — update applied

Updated customer record: {"id":2,"name":"Mike","country":"DE","email":"bob@example.com"}
```

> The gap before step 4 is Ollama loading the 21 GB chat model on first use.

### Example: a change that actually uses RAG

```
Customer ID: 2
What should change (you may reference TechCorp products): Bob's company moved their backups to where DataVault Enterprise keeps its primary server — update his country code accordingly.
```

Step 2 retrieves the DataVault doc ("Server locations: Warsaw (primary)…"), and
step 4 uses it to set `country` to `PL`:

```
Updated customer record: {"id":2,"name":"Bob","country":"PL","email":"bob@example.com"}
```

## cmd/react — model-driven ReAct agent

Same four steps, but the model decides when to call each tool. All four tools
(`search_docs`, `list_customers`, `find_customer`, `update_customer`) are
exposed over MCP and fetched into the agent via eino's MCP tool component.

Unlike `cmd/graph` (one finite pipeline run), this is an **interactive loop**:
it keeps prompting and answering — preserving conversation history so follow-up
requests have context — until you type `exit` (or press Ctrl+D).

```sh
go run ./cmd/react
```

### Example

```
TechCorp agent ready. Type a request, or 'exit' to quit.

> Update customer 3 — Carol switched to SecureMail Business, set her email to a custom techcorp.com address: carol@techcorp.com

<agent reasons, then calls search_docs → find_customer → update_customer>

Updated customer 3: email changed from carol@example.com to carol@techcorp.com.
Name and country (US) left unchanged.

> and now move her country to PL

Updated customer 3: country changed from US to PL. Email and name left unchanged.

> exit
bye
```

The agent prints its final confirmation message after each request. Tool calls
happen inside the ReAct loop (bounded by `MaxStep: 24`).

## Notes

- The customer database is **in-memory** — changes live only for the duration
  of one `cmd/graph` / `cmd/react` process. Seed customers: Alice (1, PL),
  Bob (2, DE), Carol (3, US).
- First chat-model call per process is slow (model load). Subsequent calls
  reuse the loaded model.
- The original Qdrant stack still works independently: `go run ./cmd/seeder`.

## Layout

```
cmd/seeder      original Qdrant seeder + RAG server (pkg/agent)
cmd/seederv2    eino Milvus seeder            (pkg/agentv2)
cmd/graph       eino 4-node graph pipeline    (pkg/agentv2)
cmd/react       eino ReAct agent             (pkg/agentv2)
pkg/agent       original stack: Ollama/Qdrant clients, RAG handler, MCP tools
pkg/agentv2     eino stack: models, chunker, Milvus RAG, MCP server + tools
```
