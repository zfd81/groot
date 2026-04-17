package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPRequest handles HTTP request operations
type HTTPRequest struct {
	deniedDomains   []string
	timeout         int
	maxResponseSize int
}

// NewHTTPRequest creates a new HTTP request handler
func NewHTTPRequest(deniedDomains []string, timeout, maxResponseSize int) *HTTPRequest {
	return &HTTPRequest{
		deniedDomains:   deniedDomains,
		timeout:         timeout,
		maxResponseSize: maxResponseSize,
	}
}

// isDomainDenied checks if domain is in denied list
func (h *HTTPRequest) isDomainDenied(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return true
	}

	host := parsed.Hostname()
	for _, denied := range h.deniedDomains {
		if host == denied || strings.HasPrefix(host, denied+".") {
			return true
		}
	}
	return false
}

// HTTPGet sends GET request
func (h *HTTPRequest) HTTPGet(rawURL string, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Limit response size
	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(body),
	}, nil
}

// HTTPPost sends POST request
func (h *HTTPRequest) HTTPPost(rawURL string, body interface{}, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	// Serialize body
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("POST", rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}, nil
}

// HTTPPut sends PUT request
func (h *HTTPRequest) HTTPPut(rawURL string, body interface{}, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("PUT", rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}, nil
}

// HTTPDelete sends DELETE request
func (h *HTTPRequest) HTTPDelete(rawURL string, headers map[string]string) (map[string]interface{}, error) {
	if h.isDomainDenied(rawURL) {
		return nil, fmt.Errorf("domain denied: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(h.timeout) * time.Second}

	req, err := http.NewRequest("DELETE", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, int64(h.maxResponseSize)*1024*1024)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}, nil
}
