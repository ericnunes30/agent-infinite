package capabilities

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type TestResult struct {
	OK        bool     `json:"ok"`
	Transport string   `json:"transport"`
	ToolCount int      `json:"toolCount"`
	Tools     []string `json:"tools"`
}

type rpcResponse struct {
	Result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"result"`
	Error any `json:"error"`
}

func TestMCP(ctx context.Context, item Item) (TestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if rawURL, _ := item.Spec["url"].(string); rawURL != "" {
		return testHTTP(ctx, item.Spec, rawURL)
	}
	return testStdio(ctx, item.Spec)
}

func testHTTP(ctx context.Context, spec map[string]any, endpoint string) (TestResult, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	sessionID := ""
	call := func(id int, method string) (rpcResponse, error) {
		params := map[string]any{}
		if method == "initialize" {
			params = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "agent-infinite", "version": "0.15.5"}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return rpcResponse{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		if headers, ok := spec["headers"].(map[string]any); ok {
			for key, value := range headers {
				req.Header.Set(key, fmt.Sprint(value))
			}
		}
		res, err := client.Do(req)
		if err != nil {
			return rpcResponse{}, err
		}
		defer res.Body.Close()
		if value := res.Header.Get("Mcp-Session-Id"); value != "" {
			sessionID = value
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return rpcResponse{}, fmt.Errorf("MCP returned HTTP %d", res.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if err != nil {
			return rpcResponse{}, err
		}
		if strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "data:") {
					data = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
					break
				}
			}
		}
		var response rpcResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return response, err
		}
		if response.Error != nil {
			return response, fmt.Errorf("MCP RPC error")
		}
		return response, nil
	}
	if _, err := call(1, "initialize"); err != nil {
		return TestResult{}, err
	}
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	notificationRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(notification))
	notificationRequest.Header.Set("Content-Type", "application/json")
	notificationRequest.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		notificationRequest.Header.Set("Mcp-Session-Id", sessionID)
	}
	if headers, ok := spec["headers"].(map[string]any); ok {
		for key, value := range headers {
			notificationRequest.Header.Set(key, fmt.Sprint(value))
		}
	}
	if response, notifyErr := client.Do(notificationRequest); notifyErr == nil {
		response.Body.Close()
	}
	response, err := call(2, "tools/list")
	if err != nil {
		return TestResult{}, err
	}
	return toolResult("http", response), nil
}

func testStdio(ctx context.Context, spec map[string]any) (TestResult, error) {
	command, _ := spec["command"].(string)
	if command == "" {
		return TestResult{}, errors.New("MCP spec requires url or command")
	}
	args := []string{}
	if values, ok := spec["args"].([]any); ok {
		for _, value := range values {
			args = append(args, fmt.Sprint(value))
		}
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	if values, ok := spec["env"].(map[string]any); ok {
		for key, value := range values {
			cmd.Env = append(cmd.Env, key+"="+fmt.Sprint(value))
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return TestResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return TestResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return TestResult{}, err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(bufio.NewReader(stdout))
	request := func(id int, method string, params map[string]any) (rpcResponse, error) {
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			return rpcResponse{}, err
		}
		var result rpcResponse
		if err := decoder.Decode(&result); err != nil {
			return result, err
		}
		if result.Error != nil {
			return result, errors.New("MCP RPC error")
		}
		return result, nil
	}
	if _, err := request(1, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "agent-infinite", "version": "0.15.5"}}); err != nil {
		return TestResult{}, err
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return TestResult{}, err
	}
	response, err := request(2, "tools/list", map[string]any{})
	if err != nil {
		return TestResult{}, err
	}
	return toolResult("stdio", response), nil
}

func toolResult(transport string, response rpcResponse) TestResult {
	names := make([]string, 0, len(response.Result.Tools))
	for _, tool := range response.Result.Tools {
		names = append(names, tool.Name)
	}
	return TestResult{OK: true, Transport: transport, ToolCount: len(names), Tools: names}
}
