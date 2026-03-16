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
- Python(for RAG in future)

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