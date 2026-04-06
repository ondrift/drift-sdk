package drift

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

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
