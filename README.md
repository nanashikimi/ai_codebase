# AI Codebase Copilot

Small local AI assistant for exploring code repositories.

User asks question about code.
Model searches repo using tools.
Answer returned with real file:line references.

Project made mainly to learn how LLM agents work and how models can inspect code using tools.



# Features

- User asks questions about repository
- Model searches code using ripgrep
- Opens file snippets automatically
- Answers include real code citations
- Works with local LLM through Ollama
- UI available through Open WebUI

# Stack

- Go
- Ollama
- Open WebUI
- ripgrep
- Docker
- Python (retrieval service for semantic search)

# Run locally

Start Ollama and pull model:

```bash
ollama pull qwen2.5:3b
```

Build and run server:
```bash
go build ./cmd/mcp-server
./mcp-server
```

Run Open WebUI:
```bash
docker run -d -p 3000:8080 \
-e OPENAI_API_BASE_URL=http://host.docker.internal:8081/v1 \
-e OPENAI_API_KEY=dummy \
ghcr.io/open-webui/open-webui:main
```

Open browser: http://localhost:3000'

# Plans/Roadmap:

- MCP-style tool registry
- Better prompt structure
- Clearer workflows
- Evaluation scripts
- Semantic search (based on RAG)

## Semantic search tool (Python retrieval service)

This repo includes an optional FastAPI retrieval service in `services/retrieval/` that builds a vector index over the repository and provides semantic search via embeddings.

- Go tool name: `semantic_search`
- Go server endpoint: `POST /tools/semantic_search`
- Retrieval service endpoints:
  - `POST /index` — build index (takes `{"root":"."}`)
  - `POST /search` — query index (takes `{"query":"...", "top_k": 8, "root":"."}`)
  - `GET /health`
- Configure retrieval base URL via env: `RETRIEVAL_URL` (default `http://127.0.0.1:8090`)
- The service stores its index in `.retrieval/` relative to the current working directory.

### Run retrieval service

From repo root:

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r services/retrieval/requirements.txt
uvicorn services.retrieval.app:app --host 127.0.0.1 --port 8090
```

### Test the tool directly (optional)

Build index:

```bash
curl -sS -X POST http://127.0.0.1:8090/index -H 'content-type: application/json' -d '{"root":"."}'
```

Search:

```bash
curl -sS -X POST http://127.0.0.1:8090/search -H 'content-type: application/json' -d '{"query":"where is the http server started", "top_k": 5, "root":"."}'
```

### End-to-end test via the agent (example prompt)

1) Start the Go server:

```bash
go build ./cmd/mcp-server
./mcp-server
```

2) Example request to the agent via the OpenAI-compatible endpoint:

```bash
curl -sS http://127.0.0.1:8081/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{
    "model":"qwen2.5:3b",
    "messages":[
      {"role":"user","content":"Use the semantic_search tool to find where the HTTP server is started in this repository. Then open the most relevant file with open_file and answer with at least one real path:line citation."}
    ]
  }'
```