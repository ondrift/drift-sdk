package drift

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPResponse is the response from an outbound HTTP request.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}

// HTTPRequest makes an outbound HTTP request directly.
func HTTPRequest(method, url string, headers map[string]string, body []byte) (*HTTPResponse, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("drift: create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drift: http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("drift: read response: %w", err)
	}

	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	return &HTTPResponse{
		Status:  resp.StatusCode,
		Headers: respHeaders,
		Body:    respBody,
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
