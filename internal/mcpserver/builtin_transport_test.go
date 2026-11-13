package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

func TestBuiltinRealHTTPClientAndMCP(t *testing.T) {
	ctx := context.Background()
	store, err := bridge.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Register("synthetic-instance-080", "fixture", "isolated", "synthetic-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Activate(target.ID, target.Generation); err != nil {
		t.Fatal(err)
	}
	keys := bridge.Keys{Client: strings.Repeat("c", 32), Admin: strings.Repeat("a", 32), Agent: strings.Repeat("g", 32)}
	httpServer := httptest.NewServer(bridge.Handler(store, keys))
	defer httpServer.Close()
	c, err := client.New(client.Config{Endpoint: httpServer.URL, Token: keys.Client})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := client.New(client.Config{Endpoint: httpServer.URL, Token: keys.Admin})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := client.New(client.Config{Endpoint: httpServer.URL, Token: keys.Agent})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the registered MCP exec tool, rather than only its HTTP helper.
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(c).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for i, command := range []string{"rm -rf /tmp/fixture", "mysql -e 'DROP TABLE fixture'", "redis-cli FLUSHALL", "python3 -c 'import shutil; shutil.rmtree(\"/tmp/fixture\")'"} {
		r := bridge.Submit{SessionID: target.ID, Generation: target.Generation, RequestID: fmt.Sprint("deny-", i), Command: command, Cwd: "/", TimeoutMS: 15000}
		if err := c.Call(ctx, "POST", "/v1/jobs", r, nil); err == nil || !strings.Contains(err.Error(), "COMMAND_DENIED") {
			t.Fatalf("HTTP did not deny %q: %v", command, err)
		}
		out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: r})
		if err != nil || !out.IsError || !strings.Contains(out.Content[0].(*mcp.TextContent).Text, "COMMAND_DENIED") {
			t.Fatalf("MCP did not deny %q: %+v %v", command, out, err)
		}
		out, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "explain_command", Arguments: bridge.ExplainRequest{SessionID: target.ID, Generation: target.Generation, Command: command, Cwd: "/", TimeoutMS: 15000}})
		if err != nil || out.IsError || !strings.Contains(out.Content[0].(*mcp.TextContent).Text, `"decision":"blocked"`) {
			t.Fatalf("MCP explanation diverged: %+v %v", out, err)
		}
	}
	if len(store.Jobs()) != 0 {
		t.Fatal("hard-denied submissions created jobs")
	}
	if err := agent.Call(ctx, "GET", "/v1/command-policy", nil, nil); err == nil {
		t.Fatal("agent read client catalogue")
	}
	if err := c.Call(ctx, "POST", "/v1/command-policy", nil, nil); err == nil {
		t.Fatal("catalogue accepted POST")
	}
	oldPending := ""
	count := 0
	for _, enabled := range []bool{true, false, true} {
		policy := store.BuiltinPolicy()
		if policy.Enabled != enabled {
			if err = admin.Call(ctx, "POST", "/v1/admin/policy", bridge.PolicyChange{Expected: policy, Enabled: enabled}, nil); err != nil {
				t.Fatal(err)
			}
		}
		out, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_command_policy", Arguments: map[string]any{}})
		if e != nil || out.IsError {
			t.Fatal(out, e)
		}
		var catalog bridge.CommandPolicy
		if e = json.Unmarshal([]byte(out.Content[0].(*mcp.TextContent).Text), &catalog); e != nil || catalog.Policy.Enabled != enabled || len(catalog.Categories) == 0 {
			t.Fatal(catalog, e)
		}
		for _, candidate := range []*client.Client{c, agent} {
			if e := candidate.Call(ctx, "POST", "/v1/admin/policy", bridge.PolicyChange{Expected: store.BuiltinPolicy(), Enabled: !enabled}, nil); e == nil {
				t.Fatal("non operator toggled policy")
			}
		}
		for _, command := range []string{"hostname", "hostname; rm /tmp/fixture", "df -h && free -h", "tail -n 20 '/tmp/synthetic file' | grep -F 'warn'", "cat /tmp/synthetic | sh", "ls -lh /tmp", "tail -c 16777216 /tmp/synthetic | grep -F fixture", "timeout -k 2s 5s grep -F -m 1 fixture /tmp/synthetic"} {
			count++
			r := bridge.Submit{SessionID: target.ID, Generation: target.Generation, RequestID: fmt.Sprint(count), Command: command, Cwd: "/", TimeoutMS: 15000}
			expected := "pending"
			if enabled && command != "hostname; rm /tmp/fixture" && command != "cat /tmp/synthetic | sh" {
				expected = "queued"
			}
			var job bridge.Job
			if count%2 == 0 {
				if err = c.Call(ctx, "POST", "/v1/jobs", r, &job); err != nil {
					t.Fatal(err)
				}
			} else {
				out, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: r})
				if e != nil || out.IsError {
					t.Fatal(out, e)
				}
				if err = json.Unmarshal([]byte(out.Content[0].(*mcp.TextContent).Text), &job); err != nil {
					t.Fatal(err)
				}
			}
			if job.Status != expected {
				t.Fatalf("enabled=%v command=%s job=%+v", enabled, command, job)
			}
			var explanation bridge.ApprovalExplanation
			if err = c.Call(ctx, "POST", "/v1/explain", bridge.ExplainRequest{SessionID: r.SessionID, Generation: r.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS}, &explanation); err != nil {
				t.Fatal(err)
			}
			if (explanation.Decision == "builtin") != (expected == "queued") {
				t.Fatal(explanation, job)
			}
			if !enabled && command == "hostname" {
				oldPending = job.ID
			}
			if enabled && oldPending != "" {
				old, e := store.GetJob(oldPending)
				if e != nil || old.Status != "pending" {
					t.Fatal("retroactive approval", old, e)
				}
			}
		}
	}
}
