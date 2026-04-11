# drift-sdk

Go SDK for writing Drift atomic functions. Provides the `drift.Run()` entry point and backbone service wrappers. Based purely on stdlib: zero dependencies.

## Usage

```go
package main

import "drift-sdk"

func main() {
    drift.Run(handler)
}

func handler(req drift.Request) drift.Response {
    // Read from backbone NoSQL
    docs, _ := drift.NoSQL.Collection("menu").Read("all-items")

    return drift.Response{
        Status:  200,
        Payload: docs,
    }
}
```

## How it works

`drift.Run()` detects the runtime environment:

- **Deployed mode** (`DRIFT_RUNTIME` set) — Reads a JSON request from stdin, calls the handler, writes the JSON response to stdout. This is how functions run inside the runner subprocess.
- **Local dev mode** (no env var) — Starts a local HTTP server on `PORT` (default 8080) so you can test functions with `curl` during development.

## Backbone API

### NoSQL

```go
drift.NoSQL.Collection("orders").Insert(doc)           // returns (key, error)
drift.NoSQL.Collection("orders").Read("key-123")       // returns (json.RawMessage, error)
drift.NoSQL.Collection("orders").List(filter)           // returns ([]json.RawMessage, error)
drift.NoSQL.Collection("orders").Drop()                 // returns error
```

### Cache

```go
drift.Cache.Set("key", value, ttlSeconds)   // returns error
drift.Cache.Get("key")                      // returns ([]byte, error)
drift.Cache.Del("key")                      // returns error
```

### Queue

```go
drift.Queue("order-queue").Push(body)   // returns error
drift.Queue("order-queue").Pop()        // returns (json.RawMessage, error)
```

### Secret

```go
drift.Secret.Get("API_KEY")                // returns (string, error)
drift.Secret.Set("API_KEY", "value")       // returns error
drift.Secret.Delete("API_KEY")             // returns error
```

### Blob

```go
drift.Blob.Put("file.pdf", data)   // returns error
drift.Blob.Get("file.pdf")         // returns ([]byte, error)
```

### Lock

```go
drift.Lock.Acquire("resource", ttlSeconds)   // returns (token, error)
drift.Lock.Release("resource", token)        // returns error
```

### Vector

```go
drift.Vector.Collection("embeddings").Insert(id, vector, metadata)   // returns error
drift.Vector.Collection("embeddings").Search(vector, topK)           // returns ([]json.RawMessage, error)
```

### Other

| Function | Description |
|----------|-------------|
| `HTTPGet(url, headers)` / `HTTPPost(url, headers, body)` | Make outbound HTTP requests |
| `Log(msg)` / `LogError(msg)` / `LogWarn(msg)` | Structured logging |
| `Env(key)` | Read an environment variable (secret) |

## Execution model

Each Drift atomic function runs as a subprocess inside a shared runner pod. Each incoming request spawns a fresh process with its own isolated memory, stdin, and stdout. Multiple requests are handled concurrently.

This means:

- **Requests run in parallel.** Your function can serve multiple concurrent requests without blocking.
- **No shared state between requests.** Each invocation starts with a clean process. There are no global variables or in-memory state that persists across requests — use Backbone (cache, NoSQL, queues) for shared state.

## Local development

When running locally with `drift atomic run`, the SDK starts a local HTTP server and provides an **in-memory backbone** that supports all operations (NoSQL, cache, queues, blobs, locks, secrets, logging, environment variables). Outbound HTTP requests (`HTTPGet`, `HTTPPost`) work against real endpoints.

All in-memory state is lost when the process exits — this is intentional for development. Secrets in local mode are read from environment variables (loaded from your `.env` file by `drift atomic run`).

## Module

```
module drift-sdk
go 1.25
```

No external dependencies — the SDK is self-contained.
