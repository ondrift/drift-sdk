# drift-sdk

Go SDK for writing Drift atomic functions. Provides the `drift.Run()` entry point and host call wrappers for backbone services.

## Usage

```go
package main

import "drift-sdk"

func main() {
    drift.Run(handler)
}

func handler(req drift.Request) drift.Response {
    // Read from backbone NoSQL
    docs, _ := drift.BackboneRead("menu", "all-items")

    return drift.Response{
        Status:  200,
        Payload: docs,
    }
}
```

## How it works

`drift.Run()` detects the runtime environment:

- **WASM mode** (`DRIFT_WASM_RUNTIME` set) — Reads a JSON request from stdin, calls the handler, writes the JSON response to stdout. This is how functions run inside the wasm-runner.
- **Local dev mode** (no env var) — Starts a local HTTP server on `PORT` (default 8080) so you can test functions with `curl` during development.

## Backbone helpers

| Function | Description |
|----------|-------------|
| `BackboneWrite(collection, doc)` | Write a document to a NoSQL collection |
| `BackboneRead(collection, key)` | Read a document by key |
| `BackboneList(collection, filter)` | List documents, optionally filtered by field=value |
| `BackboneDrop(collection)` | Drop an entire collection |
| `SecretGet(name)` | Retrieve a secret |
| `QueuePush(queue, message)` | Push a message to a queue |
| `QueuePop(queue)` | Pop a message from a queue |
| `BlobPut(name, data)` | Upload a blob |
| `BlobGet(name)` | Download a blob |
| `CacheSet(key, value, ttl)` | Set a cache entry |
| `CacheGet(key)` | Get a cache entry |
| `CacheDel(key)` | Delete a cache entry |
| `LockAcquire(name, ttl)` | Acquire a distributed lock |
| `LockRelease(name, token)` | Release a lock |
| `VectorInsert(collection, id, vector, metadata)` | Insert a vector embedding |
| `VectorSearch(collection, vector, topK)` | Similarity search |
| `HTTPGet(url, headers)` / `HTTPPost(url, headers, body)` | Make outbound HTTP requests |
| `Log(msg)` / `LogError(msg)` / `LogWarn(msg)` | Structured logging |
| `Env(key)` | Read an environment variable (secret) |

## Execution model

Each Drift atomic function runs inside its own Kubernetes pod. The WASM binary is compiled once and cached — every incoming request gets a **fresh module instance** with its own isolated memory, stdin, and stdout. Multiple requests are handled concurrently (one goroutine per request, each with its own WASM instance).

This means:

- **Requests run in parallel.** Your function can serve multiple concurrent requests without blocking.
- **No shared state between requests.** Each invocation starts with a clean module instance. There are no global variables or in-memory state that persists across requests — use Backbone (cache, NoSQL, queues) for shared state.
- **Each function gets its own pod.** Different functions run in separate pods and are fully isolated from each other.

## Local development

When running locally with `drift atomic run`, the SDK starts a local HTTP server and provides an **in-memory backbone** that supports all operations (NoSQL, cache, queues, blobs, locks, secrets, logging, environment variables). Outbound HTTP requests (`HTTPGet`, `HTTPPost`) work against real endpoints.

All in-memory state is lost when the process exits — this is intentional for development. Secrets in local mode are read from environment variables (loaded from your `.env` file by `drift atomic run`).

## Host calls

In WASM mode, backbone calls go through host call imports (`hostcalls_wasm.go`) that the wasm-runner provides. In local dev mode, an in-memory implementation handles all backbone operations (`hostcalls_native.go`).

## Module

```
module drift-sdk
go 1.25
```

No external dependencies — the SDK is self-contained.
