package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HTTPHealthcheckTool performs an HTTP request to a given URL.
type HTTPHealthcheckTool struct {
	client *http.Client
}

type HTTPArgs struct {
	URL string `json:"url"`
}

func NewHTTPHealthcheckTool() *HTTPHealthcheckTool {
	return &HTTPHealthcheckTool{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (h *HTTPHealthcheckTool) Name() string {
	return "http_healthcheck"
}

func (h *HTTPHealthcheckTool) Description() string {
	return "Performs an HTTP GET request to check if a web service is responding."
}

func (h *HTTPHealthcheckTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "The full URL to check (e.g. https://example.com/health)"
			}
		},
		"required": ["url"]
	}`)
}

func (h *HTTPHealthcheckTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var parsedArgs HTTPArgs
	if err := json.Unmarshal(args, &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedArgs.URL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := h.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		return fmt.Sprintf("Failed to reach %s in %v: %v", parsedArgs.URL, duration, err), nil
	}
	defer resp.Body.Close()

	return fmt.Sprintf("HTTP %d reached %s in %v", resp.StatusCode, parsedArgs.URL, duration), nil
}

// TCPHealthcheckTool checks if a TCP port is open.
type TCPHealthcheckTool struct{}

type TCPArgs struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func NewTCPHealthcheckTool() *TCPHealthcheckTool {
	return &TCPHealthcheckTool{}
}

func (t *TCPHealthcheckTool) Name() string {
	return "tcp_healthcheck"
}

func (t *TCPHealthcheckTool) Description() string {
	return "Checks if a specific TCP port is open on a host."
}

func (t *TCPHealthcheckTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"host": {
				"type": "string",
				"description": "The hostname or IP address"
			},
			"port": {
				"type": "integer",
				"description": "The TCP port number to check"
			}
		},
		"required": ["host", "port"]
	}`)
}

func (t *TCPHealthcheckTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var parsedArgs TCPArgs
	if err := json.Unmarshal(args, &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	target := fmt.Sprintf("%s:%d", parsedArgs.Host, parsedArgs.Port)
	start := time.Now()

	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	duration := time.Since(start)

	if err != nil {
		return fmt.Sprintf("Failed to connect to %s (TCP) in %v: %v", target, duration, err), nil
	}
	conn.Close()

	return fmt.Sprintf("Successfully connected to %s (TCP) in %v", target, duration), nil
}
