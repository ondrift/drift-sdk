package drift

import (
	"encoding/json"
	"fmt"
	"maps"
)

// callBackbone is the low-level backbone request helper. It serializes the
// request, calls the host function, and returns the raw response bytes.
func callBackbone(method, path string, body any) ([]byte, error) {
	var bodyJSON json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("drift: marshal backbone request body: %w", err)
		}
		bodyJSON = b
	}

	reqBytes, err := json.Marshal(backboneRequest{
		Method: method,
		Path:   path,
		Body:   bodyJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("drift: marshal backbone request: %w", err)
	}

	reqPtr, reqLen := bytesToPtr(reqBytes)
	respLen := hostBackboneRequest(reqPtr, reqLen)
	if respLen == 0 {
		return nil, nil
	}
	return readHostResponse(respLen), nil
}

// --- NoSQL ---

// BackboneWrite writes a document to a Backbone NoSQL collection.
// Returns the assigned key on success.
func BackboneWrite(collection string, doc any) (string, error) {
	payload := map[string]any{
		"collection": collection,
	}
	// Merge doc fields into payload if it's a map.
	if m, ok := doc.(map[string]any); ok {
		maps.Copy(payload, m)
	} else {
		payload["data"] = doc
	}

	resp, err := callBackbone("POST", "write", payload)
	if err != nil {
		return "", err
	}

	// Backbone may return JSON with a key field or plain text on success.
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
// Optionally filter by a single field=value match.
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
// Returns a token that must be passed to LockRelease.
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
