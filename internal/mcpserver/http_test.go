package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestSharedHTTPAuthorizationAndClients(t *testing.T) {
	key := strings.Repeat("c", 32)
	s, err := bridge.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	c, err := client.New(client.Config{Endpoint: "http://" + ts.Listener.Addr().String(), Token: key})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = bridge.HandlerWithMCP(s, bridge.Keys{Client: key, Admin: "admin", Agent: "agent"}, func() {}, HTTPHandler(c))
	ts.Start()
	defer ts.Close()
	target, err := s.Register("shared-http-fixture", "fixture", "isolated", "fixture-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(target.ID, target.Generation); err != nil {
		t.Fatal(err)
	}
	input := bridge.Submit{SessionID: target.ID, Generation: target.Generation, RequestID: "shared-request", Command: "echo fixture", Cwd: "/", TimeoutMS: 15000}
	var original string
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {"agent", "", 403}, {key, "https://evil.example", 403}} {
		r, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatal("auth", resp.StatusCode)
		}
	}
	// Separate conversations share one HTTP server, without a stdio process or retained session.
	for i := 0; i < 3; i++ {
		mc := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
		session, e := mc.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{key}}}, nil)
		if e != nil {
			t.Fatal(e)
		}
		result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_sessions", Arguments: map[string]any{}})
		if e != nil || result.IsError {
			t.Fatal(result, e)
		}
		result, e = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "exec", Arguments: input})
		if e != nil || result.IsError {
			t.Fatal(result, e)
		}
		var job bridge.Job
		if e = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &job); e != nil {
			t.Fatal(e)
		}
		if job.Status != "pending" || (original != "" && original != job.ID) {
			t.Fatal("approval/idempotency bypass", job)
		}
		original = job.ID
		result, e = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "wait_job", Arguments: map[string]any{"id": job.ID, "wait_ms": 20}})
		if e != nil || result.IsError {
			t.Fatal(result, e)
		}
		if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, `"timed_out":true`) {
			t.Fatal(result)
		}
		session.Close()
	}
}
