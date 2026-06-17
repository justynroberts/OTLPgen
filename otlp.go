package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Client posts OTLP/HTTP JSON payloads to an OTLP-compatible backend.
type Client struct {
	baseURL string
	headers map[string]string
	http    *http.Client
}

func NewClient(baseURL string, headers map[string]string, verifySSL bool) *Client {
	transport := &http.Transport{}
	if !verifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		baseURL: baseURL,
		headers: headers,
		http:    &http.Client{Timeout: 10 * time.Second, Transport: transport},
	}
}

// Send marshals payload to JSON and POSTs it to baseURL+endpoint.
// Returns true on a 200/202 response.
func (c *Client) Send(endpoint string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		logError("marshal %s: %v", endpoint, err)
		return false
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		logError("build request %s: %v", endpoint, err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		logError("OTLP %s error: %v", endpoint, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		return true
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	logWarn("OTLP %s failed: %d - %s", endpoint, resp.StatusCode, string(snippet))
	return false
}
