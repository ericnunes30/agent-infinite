package hookbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxHookPayload = 1 << 20

// Forward reads one provider hook payload and sends it to the session-scoped
// loopback callback. The endpoint and credentials come only from inherited
// environment variables; they never appear in hook definitions or arguments.
func Forward(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(stdin, maxHookPayload+1))
	if err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	if len(raw) > maxHookPayload {
		return errors.New("hook input exceeds 1 MiB")
	}
	callback := Callback{
		SessionID:   os.Getenv("AGENT_INFINITE_HOOK_SESSION_ID"),
		NodeID:      os.Getenv("AGENT_INFINITE_NODE_ID"),
		WorkspaceID: os.Getenv("AGENT_INFINITE_WORKSPACE_ID"),
		Provider:    os.Getenv("AGENT_INFINITE_PROVIDER"), Raw: raw,
	}
	data, err := json.Marshal(callback)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(os.Getenv("AGENT_INFINITE_BACKEND_URL"), "/") + "/internal/hooks/events"
	token := os.Getenv("AGENT_INFINITE_HOOK_TOKEN")
	if endpoint == "/internal/hooks/events" || token == "" || callback.SessionID == "" {
		return errors.New("hook session environment is incomplete")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if requestErr != nil {
			return requestErr
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Agent-Infinite-Hook-Token", token)
		response, doErr := client.Do(request)
		if doErr == nil {
			responseData, _ := io.ReadAll(io.LimitReader(response.Body, maxHookPayload))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				var accepted struct {
					HookOutput string `json:"hookOutput"`
				}
				if json.Unmarshal(responseData, &accepted) == nil && accepted.HookOutput != "" && stdout != nil {
					_, _ = io.WriteString(stdout, accepted.HookOutput)
				}
				return nil
			}
			if response.StatusCode == http.StatusConflict {
				return nil
			}
			lastErr = fmt.Errorf("hook callback returned %s", response.Status)
		} else {
			lastErr = doErr
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "agent-infinite hook callback: %v\n", lastErr)
	}
	return lastErr
}
