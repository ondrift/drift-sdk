package drift

import (
	"encoding/json"
	"fmt"
)

// HTTPResponse is the response from an outbound HTTP request.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}

// HTTPRequest makes an outbound HTTP request through the WASM runner host.
// This is the only way for WASM functions to reach external services.
func HTTPRequest(method, url string, headers map[string]string, body []byte) (*HTTPResponse, error) {
	var bodyJSON json.RawMessage
	if body != nil {
		bodyJSON = body
	}

	reqBytes, err := json.Marshal(httpRequest{
		Method:  method,
		URL:     url,
		Headers: headers,
		Body:    bodyJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("drift: marshal http request: %w", err)
	}

	reqPtr, reqLen := bytesToPtr(reqBytes)
	respLen := hostHTTPRequest(reqPtr, reqLen)
	if respLen == 0 {
		return nil, fmt.Errorf("drift: empty http response")
	}

	respBytes := readHostResponse(respLen)

	var resp httpResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("drift: parse http response: %w", err)
	}

	return &HTTPResponse{
		Status:  resp.Status,
		Headers: resp.Headers,
		Body:    resp.Body,
	}, nil
}

// HTTPGet is a convenience wrapper for GET requests.
func HTTPGet(url string, headers map[string]string) (*HTTPResponse, error) {
	return HTTPRequest("GET", url, headers, nil)
}

// HTTPPost is a convenience wrapper for POST requests with a JSON body.
func HTTPPost(url string, headers map[string]string, body any) (*HTTPResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("drift: marshal http body: %w", err)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if _, ok := headers["Content-Type"]; !ok {
		headers["Content-Type"] = "application/json"
	}
	return HTTPRequest("POST", url, headers, b)
}
