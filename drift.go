package drift

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Request is the incoming HTTP request passed to the function handler.
// The runner serializes the original HTTP request into this struct
// and writes it to the subprocess's stdin as JSON.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	Body    json.RawMessage   `json:"body"`
}

// Response is what the function handler returns. The runner reads
// this from the subprocess's stdout and converts it back into an HTTP response.
type Response struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Payload json.RawMessage `json:"payload"`
}

// Run is the entry point for Drift Atomic functions. The handler receives
// the incoming HTTP request and must return a response.
//
// In deployed mode (DRIFT_RUNTIME is set): reads a JSON request from stdin,
// calls the handler, and writes the JSON response to stdout. The runner
// manages the HTTP routing, log capture, and metrics.
//
// In local dev mode (no DRIFT_RUNTIME): starts a local HTTP server on
// the port specified by the PORT env var (default 8080) so developers can
// test their functions with `drift atomic run`.
func Run(handler func(Request) Response) {
	if os.Getenv("DRIFT_RUNTIME") != "" {
		runDeployed(handler)
	} else {
		runLocal(handler)
	}
}

// runDeployed implements the deployed-mode protocol: read request from stdin,
// call handler, write response to stdout.
func runDeployed(handler func(Request) Response) {
	var req Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		resp := Response{
			Status:  http.StatusBadRequest,
			Message: "failed to decode request",
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		return
	}

	resp := handler(req)
	json.NewEncoder(os.Stdout).Encode(resp)
}

// runLocal starts a local HTTP server for development and testing.
func runLocal(handler func(Request) Response) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}

		headers := make(map[string]string)
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}

		req := Request{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: headers,
			Query:   r.URL.RawQuery,
			Body:    body,
		}

		resp := handler(req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.Status)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  resp.Status,
			"message": resp.Message,
			"payload": resp.Payload,
		})
	})

	fmt.Fprintf(os.Stderr, "drift-sdk: local server starting on :%s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, "drift-sdk: server error: %v\n", err)
		os.Exit(1)
	}
}

//       Local backbone section          //

type memBackbone struct {
	mu     sync.Mutex
	nosql  map[string]map[string]json.RawMessage
	cache  map[string]json.RawMessage
	queues map[string][]json.RawMessage
	blobs  map[string][]byte
	locks  map[string]string
	nextID int
}

// localBackbone is an in-memory implementation of backbone services for local
// development with `drift atomic run`. All state lives in memory and is lost
// when the process exits.
var localBackbone = &memBackbone{
	nosql:  make(map[string]map[string]json.RawMessage),
	cache:  make(map[string]json.RawMessage),
	queues: make(map[string][]json.RawMessage),
	blobs:  make(map[string][]byte),
	locks:  make(map[string]string),
}

// backboneRequest is the internal envelope for backbone calls (local dev only).
type BackboneRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

func (m *memBackbone) handle(req BackboneRequest) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := req.Path
	method := strings.ToUpper(req.Method)

	var query url.Values
	if i := strings.IndexByte(path, '?'); i >= 0 {
		query, _ = url.ParseQuery(path[i+1:])
		path = path[:i]
	}

	switch {
	// --- NoSQL ---
	case path == "write" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		col, _ := body["collection"].(string)
		if col == "" {
			col = "default"
		}
		if m.nosql[col] == nil {
			m.nosql[col] = make(map[string]json.RawMessage)
		}
		m.nextID++
		key := fmt.Sprintf("%d", m.nextID)
		doc, _ := json.Marshal(body)
		m.nosql[col][key] = doc
		resp, _ := json.Marshal(map[string]string{"key": key})
		return resp

	case path == "read" && method == "GET":
		col := query.Get("collection")
		key := query.Get("key")
		if col == "" {
			col = "default"
		}
		if docs, ok := m.nosql[col]; ok {
			if doc, ok := docs[key]; ok {
				return doc
			}
		}
		return nil

	case path == "nosql/list" && method == "GET":
		col := query.Get("collection")
		if col == "" {
			col = "default"
		}
		docs, ok := m.nosql[col]
		if !ok {
			return marshalJSON([]any{})
		}
		filterField := query.Get("field")
		filterValue := query.Get("value")
		var results []json.RawMessage
		for _, doc := range docs {
			if filterField != "" {
				var obj map[string]any
				json.Unmarshal(doc, &obj)
				v := fmt.Sprintf("%v", obj[filterField])
				if v != filterValue {
					continue
				}
			}
			results = append(results, doc)
		}
		if results == nil {
			results = []json.RawMessage{}
		}
		return marshalJSON(results)

	case strings.HasPrefix(path, "nosql/drop") && method == "POST":
		col := query.Get("collection")
		delete(m.nosql, col)
		return nil

	// --- Cache ---
	case path == "cache/set" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		key, _ := body["key"].(string)
		val, _ := json.Marshal(body["value"])
		m.cache[key] = val
		return nil

	case path == "cache/get" && method == "GET":
		key := query.Get("key")
		if val, ok := m.cache[key]; ok {
			return val
		}
		return nil

	case strings.HasPrefix(path, "cache/del"):
		key := query.Get("key")
		delete(m.cache, key)
		return nil

	// --- Queue ---
	case path == "queue/push" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		name, _ := body["queue"].(string)
		msg, _ := json.Marshal(body["body"])
		m.queues[name] = append(m.queues[name], msg)
		return nil

	case path == "queue/pop" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		name, _ := body["queue"].(string)
		q := m.queues[name]
		if len(q) == 0 {
			return nil
		}
		msg := q[0]
		m.queues[name] = q[1:]
		return msg

	// --- Blob ---
	case path == "blob/put" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		name, _ := body["name"].(string)
		data, _ := json.Marshal(body["data"])
		m.blobs[name] = data
		return nil

	case path == "blob/get" && method == "GET":
		name := query.Get("name")
		if data, ok := m.blobs[name]; ok {
			return data
		}
		return nil

	// --- Secret ---
	case path == "secret/get" && method == "GET":
		return nil

	// --- Lock ---
	case path == "lock/acquire" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		name, _ := body["name"].(string)
		if _, held := m.locks[name]; held {
			return nil
		}
		m.nextID++
		token := fmt.Sprintf("local-lock-%d", m.nextID)
		m.locks[name] = token
		return marshalJSON(map[string]string{"token": token})

	case path == "lock/release" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		name, _ := body["name"].(string)
		delete(m.locks, name)
		return nil

	// --- Vector ---
	case path == "vector/insert" && method == "POST":
		return nil

	case path == "vector/search" && method == "POST":
		return marshalJSON([]any{})
	}

	return nil
}

func marshalJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// callBackbone routes backbone requests to the real service (when deployed)
// or the in-memory store (local dev).
func callBackbone(method, path string, body any) ([]byte, error) {
	if url := os.Getenv("BACKBONE_URL"); url != "" {
		return callBackboneHTTP(url, method, path, body)
	}
	return callBackboneLocal(method, path, body)
}

// callBackboneHTTP calls the real backbone service via HTTP.
func callBackboneHTTP(baseURL, method, path string, body any) ([]byte, error) {
	url := baseURL + "/" + path

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("drift: marshal backbone body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("drift: backbone request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drift: backbone call: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("drift: read backbone response: %w", err)
	}
	if resp.StatusCode == http.StatusNoContent || len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// callBackboneLocal uses the in-memory store for local dev.
func callBackboneLocal(method, path string, body any) ([]byte, error) {
	var bodyJSON json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("drift: marshal backbone request body: %w", err)
		}
		bodyJSON = b
	}

	resp := localBackbone.handle(BackboneRequest{
		Method: method,
		Path:   path,
		Body:   bodyJSON,
	})
	if len(resp) == 0 {
		return nil, nil
	}
	return resp, nil
}

// --- NoSQL ---

// BackboneWrite writes a document to a Backbone NoSQL collection.
// Returns the assigned key on success.
func BackboneWrite(collection string, doc any) (string, error) {
	payload := map[string]any{
		"collection": collection,
	}
	if m, ok := doc.(map[string]any); ok {
		maps.Copy(payload, m)
	} else {
		payload["data"] = doc
	}

	resp, err := callBackbone("POST", "write", payload)
	if err != nil {
		return "", err
	}

	var result struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(resp, &result)
	return result.Key, nil
}

// BackboneRead reads a document from a Backbone NoSQL collection by key.
func BackboneRead(collection, key string) (json.RawMessage, error) {
	resp, err := callBackbone("GET", fmt.Sprintf("read?collection=%s&key=%s", collection, key), nil)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// BackboneList returns all documents in a Backbone NoSQL collection.
func BackboneList(collection string, filter map[string]string) ([]json.RawMessage, error) {
	path := fmt.Sprintf("nosql/list?collection=%s", collection)
	for k, v := range filter {
		path += fmt.Sprintf("&field=%s&value=%s", k, v)
	}
	resp, err := callBackbone("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return []json.RawMessage{}, nil
	}

	var results []json.RawMessage
	if err := json.Unmarshal(resp, &results); err != nil {
		return nil, fmt.Errorf("drift: parse list response: %w", err)
	}
	return results, nil
}

// BackboneDrop drops an entire Backbone NoSQL collection.
func BackboneDrop(collection string) error {
	_, err := callBackbone("POST", fmt.Sprintf("nosql/drop?collection=%s", collection), nil)
	return err
}

// --- Cache ---

// CacheGet retrieves a value from the Backbone cache.
func CacheGet(key string) ([]byte, error) {
	return callBackbone("GET", fmt.Sprintf("cache/get?key=%s", key), nil)
}

// CacheSet stores a value in the Backbone cache with an optional TTL.
func CacheSet(key string, value any, ttlSeconds int) error {
	payload := map[string]any{
		"key":   key,
		"value": value,
	}
	if ttlSeconds > 0 {
		payload["ttl"] = ttlSeconds
	}
	_, err := callBackbone("POST", "cache/set", payload)
	return err
}

// CacheDel removes a key from the Backbone cache.
func CacheDel(key string) error {
	_, err := callBackbone("DELETE", fmt.Sprintf("cache/del?key=%s", key), nil)
	return err
}

// --- Queue ---

// QueuePush pushes a message onto a Backbone queue.
func QueuePush(queue string, body any) error {
	_, err := callBackbone("POST", "queue/push", map[string]any{
		"queue": queue,
		"body":  body,
	})
	return err
}

// QueuePop pops a message from a Backbone queue.
func QueuePop(queue string) (json.RawMessage, error) {
	resp, err := callBackbone("POST", "queue/pop", map[string]any{
		"queue": queue,
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// --- Blob ---

// BlobPut uploads a blob to Backbone.
func BlobPut(name string, data []byte) error {
	_, err := callBackbone("POST", "blob/put", map[string]any{
		"name": name,
		"data": data,
	})
	return err
}

// BlobGet downloads a blob from Backbone.
func BlobGet(name string) ([]byte, error) {
	return callBackbone("GET", fmt.Sprintf("blob/get?name=%s", name), nil)
}

// --- Secret ---

// SecretGet retrieves a secret value from Backbone.
func SecretGet(name string) (string, error) {
	resp, err := callBackbone("GET", fmt.Sprintf("secret/get?name=%s", name), nil)
	if err != nil {
		return "", err
	}
	return string(resp), nil
}

// --- Vector ---

// VectorInsert inserts a vector into a Backbone vector collection.
func VectorInsert(collection, id string, vector []float32, metadata any) error {
	_, err := callBackbone("POST", "vector/insert", map[string]any{
		"collection": collection,
		"id":         id,
		"vector":     vector,
		"metadata":   metadata,
	})
	return err
}

// VectorSearch performs a k-nearest-neighbor search on a Backbone vector collection.
func VectorSearch(collection string, vector []float32, k int) ([]json.RawMessage, error) {
	resp, err := callBackbone("POST", "vector/search", map[string]any{
		"collection": collection,
		"vector":     vector,
		"k":          k,
	})
	if err != nil {
		return nil, err
	}

	var results []json.RawMessage
	if err := json.Unmarshal(resp, &results); err != nil {
		return nil, fmt.Errorf("drift: parse vector search response: %w", err)
	}
	return results, nil
}

// --- Lock ---

// LockAcquire acquires a distributed lock in Backbone.
func LockAcquire(name string, ttlSeconds int) (string, error) {
	resp, err := callBackbone("POST", "lock/acquire", map[string]any{
		"name": name,
		"ttl":  ttlSeconds,
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("drift: parse lock response: %w", err)
	}
	return result.Token, nil
}

// LockRelease releases a previously acquired distributed lock.
func LockRelease(name, token string) error {
	_, err := callBackbone("POST", "lock/release", map[string]any{
		"name":  name,
		"token": token,
	})
	return err
}
