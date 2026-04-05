//go:build !wasip1

package drift

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"unsafe"
)

// localBackbone is an in-memory implementation of backbone services for local
// development with `drift atomic run`. All state lives in memory and is lost
// when the process exits.
var localBackbone = &memBackbone{
	nosql:  make(map[string]map[string]json.RawMessage), // collection -> key -> doc
	cache:  make(map[string]json.RawMessage),
	queues: make(map[string][]json.RawMessage),
	blobs:  make(map[string][]byte),
	locks:  make(map[string]string),
}

type memBackbone struct {
	mu     sync.Mutex
	nosql  map[string]map[string]json.RawMessage
	cache  map[string]json.RawMessage
	queues map[string][]json.RawMessage
	blobs  map[string][]byte
	locks  map[string]string
	nextID int
}

func (m *memBackbone) handle(req backboneRequest) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := req.Path
	method := strings.ToUpper(req.Method)

	// Parse query parameters from path.
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
			return jsonMarshal([]any{})
		}
		// Collect optional field filter.
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
		return jsonMarshal(results)

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
		name := query.Get("name")
		// In local dev, secrets are available as environment variables.
		val := os.Getenv(name)
		return []byte(val)

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
		return jsonMarshal(map[string]string{"token": token})

	case path == "lock/release" && method == "POST":
		var body map[string]any
		json.Unmarshal(req.Body, &body)
		name, _ := body["name"].(string)
		delete(m.locks, name)
		return nil

	// --- Vector (basic in-memory stub) ---
	case path == "vector/insert" && method == "POST":
		return nil

	case path == "vector/search" && method == "POST":
		return jsonMarshal([]any{})
	}

	return nil
}

func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// hostBackboneRequest implements backbone calls against the in-memory store.
func hostBackboneRequest(reqPtr, reqLen uint32) uint32 {
	reqBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(reqPtr))), int(reqLen))

	var req backboneRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		return 0
	}

	resp := localBackbone.handle(req)
	if len(resp) == 0 {
		return 0
	}

	lastAlloc = make([]byte, len(resp))
	copy(lastAlloc, resp)
	return uint32(len(resp))
}

// hostHTTPRequest makes real outbound HTTP requests in local dev mode.
func hostHTTPRequest(reqPtr, reqLen uint32) uint32 {
	reqBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(reqPtr))), int(reqLen))

	var req httpRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		return 0
	}

	var bodyReader *strings.Reader
	if req.Body != nil {
		bodyReader = strings.NewReader(string(req.Body))
	} else {
		bodyReader = strings.NewReader("")
	}

	httpReq, err := http.NewRequest(req.Method, req.URL, bodyReader)
	if err != nil {
		return 0
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var buf strings.Builder
	buf.Grow(4096)
	fmt.Fprintf(&buf, `{"status":%d,"headers":{`, resp.StatusCode)
	first := true
	for k := range resp.Header {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		hk, _ := json.Marshal(k)
		hv, _ := json.Marshal(resp.Header.Get(k))
		buf.Write(hk)
		buf.WriteByte(':')
		buf.Write(hv)
	}
	buf.WriteString(`},"body":`)
	bodyBytes := make([]byte, 0, 4096)
	bodyBytes, _ = appendReadAll(bodyBytes, resp.Body)
	// Body is already bytes — write as JSON string.
	bodyJSON, _ := json.Marshal(json.RawMessage(bodyBytes))
	buf.Write(bodyJSON)
	buf.WriteByte('}')

	result := []byte(buf.String())
	lastAlloc = make([]byte, len(result))
	copy(lastAlloc, result)
	return uint32(len(result))
}

func appendReadAll(buf []byte, r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

// hostLogWrite prints log messages to stderr in local dev mode.
func hostLogWrite(levelPtr, levelLen, msgPtr, msgLen uint32) {
	level := unsafe.String((*byte)(unsafe.Pointer(uintptr(levelPtr))), int(levelLen))
	msg := unsafe.String((*byte)(unsafe.Pointer(uintptr(msgPtr))), int(msgLen))
	fmt.Fprintf(os.Stderr, "[%s] %s\n", level, msg)
}

// hostEnvGet reads environment variables in local dev mode.
func hostEnvGet(keyPtr, keyLen uint32) uint32 {
	key := unsafe.String((*byte)(unsafe.Pointer(uintptr(keyPtr))), int(keyLen))
	val := os.Getenv(key)
	if val == "" {
		return 0
	}
	lastAlloc = []byte(val)
	return uint32(len(lastAlloc))
}
