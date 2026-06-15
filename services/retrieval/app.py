from __future__ import annotations
import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable, Literal
import numpy as np
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from sentence_transformers import SentenceTransformer

try:
    import faiss  # type: ignore
except Exception as e:  # pragma: no cover
    raise RuntimeError(
        "faiss import failed. Install faiss-cpu (or faiss-gpu)."
    ) from e #exception chaining

APP_NAME = "ai-codebase-retrieval" #FastAPI app name
DEFAULT_MODEL = os.getenv("EMBED_MODEL", "sentence-transformers/all-MiniLM-L6-v2") #embedding model
INDEX_DIR = Path(os.getenv("RETRIEVAL_INDEX_DIR", ".retrieval")).resolve() #path for .retrieval index
INDEX_PATH = INDEX_DIR / "index.faiss" #faiss index
META_PATH = INDEX_DIR / "meta.json" #json with chunks' metadata
MAX_FILE_BYTES = int(os.getenv("RETRIEVAL_MAX_FILE_BYTES", "800000"))  # no indexing for large files(border ~= 0.8MB)
CHUNK_LINES = int(os.getenv("RETRIEVAL_CHUNK_LINES", "60")) #chunk size
CHUNK_OVERLAP = int(os.getenv("RETRIEVAL_CHUNK_OVERLAP", "10")) #size of possible chunk overlapping(usually 1/6 * CHUNK_LINES)
'''
ATTENTION: accurate with overlapping, it should be balanced:
too low overlap ==> important code pieces may be cut on borders ==> semantic search can lose meaning(context not full)
too much overlap ==> more repeats ==> more chunks ==> more embeddings ==>
  ==> longer indexing(since more recordings in FAISS and metadata) ==> search returns similar chunks(more noise in topK)
'''

def _safe_rel_root(root: str | None) -> Path: #limit search dir root
    r = (root or ".").strip()
    if r == "": #no empty root
        r = "."
    p = Path(r)
    if p.is_absolute():
        raise ValueError("absolute paths are not allowed")
    # Prevent leaving project through traversals like ../../
    norm = Path(os.path.normpath(r))
    if str(norm).startswith(".."):
        raise ValueError("path traversal is not allowed")
    return norm

def _iter_code_files(root: Path) -> Iterable[Path]: #filter files for indexing
    skip_dirs = {".git", "node_modules", "dist", "build", ".venv", ".retrieval"}
    allowed_ext = {".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".md", ".txt"}
    for p in root.rglob("*"):
        if any(part in skip_dirs for part in p.parts): #skip while any path part should be skipped
            continue
        if not p.is_file(): #skip if not file
            continue
        if p.suffix.lower() not in allowed_ext: # if suffix not allowed ==> skip
            continue
        try:
            if p.stat().st_size > MAX_FILE_BYTES: #too large file ==> skip
                continue
        except OSError: #caught system error ==> skip
            continue
        yield p

def _read_text_lines(p: Path) -> list[str]:
    # Should be tolerant reading for mixed encodings; cz of that, replace invalid bytes.
    raw = p.read_bytes()
    text = raw.decode("utf-8", errors="replace")
    return text.splitlines()

@dataclass(frozen=True)
class Chunk:
    path: str
    start_line: int
    end_line: int
    text: str

def _chunk_lines(path: str, lines: list[str]) -> list[Chunk]:
    if not lines:
        return []
    n = len(lines)
    size = max(5, CHUNK_LINES) # minimum 5 string by default as chunk size
    overlap = max(0, min(CHUNK_OVERLAP, size - 1)) #0 <= overlap < size
    out: list[Chunk] = []
    start = 1
    while start <= n: #while we have something to read
        end = min(n, start + size - 1) #chunk size should be less or equal to size
        text = "\n".join(lines[start - 1 : end]) #chunk content
        out.append(Chunk(path=path, start_line=start, end_line=end, text=text))
        if end == n:
            break #break stuff
        start = end - overlap + 1 #moving start for next measure(like pointer)
    return out

def _normalize(v: np.ndarray) -> np.ndarray: #not necessary, since other normalization already done, just a little safe
    denom = np.linalg.norm(v, axis=1, keepdims=True) + 1e-12 #adding something in order not to divide to 0
    return v / denom

#Pydantic models
class IndexMeta(BaseModel):
    root: str #path for index building
    model: str #embedding-model name
    dim: int #dimension of embedding
    chunks: list[dict[str, Any]]

class IndexRequest(BaseModel):#json request for index endpoint
    root: str | None = Field(default=".", description="Repo root relative to service working dir.")

#for entire models watch semantic_search.go
class SearchRequest(BaseModel):#json request for search endpoint
    query: str
    top_k: int = 8
    root: str | None = "."

class Hit(BaseModel):#single search result
    path: str
    start_line: int
    end_line: int
    score: float
    preview: str | None = None
    text: str | None = None

class SearchResponse(BaseModel):#response for FastAPI
    hits: list[Hit]

app = FastAPI(title=APP_NAME)
_model: SentenceTransformer | None = None

def _get_model() -> SentenceTransformer:#if model not declared ==> load it
    global _model
    if _model is None:
        _model = SentenceTransformer(DEFAULT_MODEL)
    return _model

def _load_index() -> tuple[faiss.Index, IndexMeta]: #load FAISS index and its metadata
    if not INDEX_PATH.exists() or not META_PATH.exists():#at least one not defined ==> raise error
        raise FileNotFoundError("index not built")
    idx = faiss.read_index(str(INDEX_PATH))
    meta = IndexMeta.model_validate_json(META_PATH.read_text("utf-8"))
    return idx, meta

@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}

@app.post("/index")
def build_index(req: IndexRequest) -> dict[str, Any]:
    try:
        rel = _safe_rel_root(req.root)
    except ValueError as e: #_safe_rel_root got error
        raise HTTPException(status_code=400, detail=str(e)) #Bad Request
    root = (Path.cwd() / rel).resolve()
    if not root.exists() or not root.is_dir():
        raise HTTPException(status_code=400, detail="root directory does not exist")
    model = _get_model()
    chunks: list[Chunk] = []
    for f in _iter_code_files(root):
        rel_path = f.relative_to(root).as_posix()#posix for compatability
        try:
            lines = _read_text_lines(f)
        except Exception: #cannot be read ==> skip
            continue
        chunks.extend(_chunk_lines(rel_path, lines)) # cut lines to chunks
    if not chunks:#empty ==> nothing to index
        raise HTTPException(status_code=400, detail="no indexable files found")
    texts = [c.text for c in chunks]
    emb = model.encode(texts, batch_size=32, show_progress_bar=False, normalize_embeddings=True)#chunks to embeddings, 32 at a time, with normalization, no progress bar
    emb = np.asarray(emb, dtype="float32")#adapt for FAISS
    if emb.ndim != 2:# chunks' amount x embedding dimension
        raise HTTPException(status_code=500, detail="unexpected embedding shape")#internal
    dim = int(emb.shape[1])
    index = faiss.IndexFlatIP(dim)#creating FAISS index-catalogue for current dimensionality, inner product search
    #inner product requires normalization(idea for future: realization through cosine similarity)
    index.add(emb)
    INDEX_DIR.mkdir(parents=True, exist_ok=True)#create .retrieval if not exists
    faiss.write_index(index, str(INDEX_PATH))
    meta = IndexMeta(root=str(rel), model=DEFAULT_MODEL, dim=dim,
        chunks=[{"path": c.path, "start_line": c.start_line, "end_line": c.end_line} for c in chunks],
    )
    META_PATH.write_text(meta.model_dump_json(), "utf-8")
    return {"ok": True, "files": len(set(c.path for c in chunks)), "chunks": len(chunks), "dim": dim}

@app.post("/search", response_model=SearchResponse)
def search(req: SearchRequest) -> SearchResponse:
    q = (req.query or "").strip()
    if q == "":
        raise HTTPException(status_code=400, detail="query is required")# bad request
    top_k = int(req.top_k or 8)
    if top_k <= 0 or top_k > 50: #8 by default
        top_k = 8
    try:
        index, meta = _load_index()
    except FileNotFoundError:
        raise HTTPException(status_code=409, detail="index not built") #"Conflict" ==> Go part should decide to build index and retry
    try:
        rel = _safe_rel_root(req.root)
    except ValueError as e: #bad root
        raise HTTPException(status_code=400, detail=str(e))
    if str(rel) != meta.root:#index root != request root
        raise HTTPException(status_code=409, detail=f"index root mismatch (have {meta.root}, want {rel})")
    model = _get_model()
    qv = model.encode([q], show_progress_bar=False, normalize_embeddings=True) # query into embedding
    #then this embedding would be compared to chunk embeddings
    qv = np.asarray(qv, dtype="float32")
    if qv.ndim != 2:#again, "table" should have 2 dimensions
        raise HTTPException(status_code=500, detail="unexpected embedding shape")#internal
    qv = _normalize(qv)# additional normalization
    scores, ids = index.search(qv, top_k)
    scores = scores[0].tolist()
    ids = ids[0].tolist() #only first single string
    hits: list[Hit] = []
    for score, idx in zip(scores, ids, strict=False):#pairwise, without strict break cause of length diff
        if idx < 0 or idx >= len(meta.chunks): #catch invalid ids
            continue
        ch = meta.chunks[idx]#metadata of found chunk
        #preview = None
        preview = f"{ch['path']}:{ch['start_line']}-{ch['end_line']}"
        hits.append(
            Hit(path=ch["path"], start_line=int(ch["start_line"]), end_line=int(ch["end_line"]),
                score=float(score), preview=preview)
        )
    return SearchResponse(hits=hits)

