package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

func TestRemovedSessionsDisappearFromSharedCLIAndMCPDiscovery(t *testing.T) {
	s, err := bridge.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.Register("manual-fixture-instance", "fixture terminal", "isolated", "fixture-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(x.ID, x.Generation); err != nil {
		t.Fatal(err)
	}
	x = s.Sessions()[0]
	server := httptest.NewServer(bridge.Handler(s, bridge.Keys{Client: "fixture-client", Admin: "fixture-admin"}))
	defer server.Close()
	c := &client.Client{Config: client.Config{Endpoint: server.URL, Token: "fixture-client"}, HTTP: server.Client()}
	ctx := context.Background()
	check := func(want string) {
		t.Helper()
		// CLI and MCP use this same client resolver and authenticated HTTP list.
		out, err := c.ResolveSession(ctx, x.Name, x.Environment)
		if err != nil || out.Status != want {
			t.Fatalf("CLI resolver: %+v %v", out, err)
		}
		result, _, err := resolveSession(ctx, c, ResolveSessionInput{Name: "fixture", Fuzzy: true})
		if err != nil || result.IsError {
			t.Fatalf("MCP resolver: %+v %v", result, err)
		}
		var resolved client.SessionResolution
		if err = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &resolved); err != nil {
			t.Fatal(err)
		}
		if want == "not_found" && (resolved.Status != "not_found" || len(resolved.Candidates) != 0) {
			t.Fatal("MCP retained archived candidate")
		}
	}
	check("matched")
	if err = s.RemoveSessions(true, []bridge.RemovalItem{{ID: x.ID, Generation: x.Generation, State: x.State, Fingerprint: x.Fingerprint, Name: x.Name, Environment: x.Environment, Jobs: []bridge.RemovalJob{}}}); err != nil {
		t.Fatal(err)
	}
	check("not_found")
	result, _, err := call(ctx, c, "GET", "/v1/sessions", nil)
	if err != nil || result.IsError || result.Content[0].(*mcp.TextContent).Text != "[]" {
		t.Fatalf("MCP list: %+v %v", result, err)
	}
	if err = c.Call(ctx, "GET", "/v1/admin/sessions?include_archived=true", nil, nil); err == nil {
		t.Fatal("client accessed admin history projection")
	}
	if err = c.Call(ctx, "POST", "/v1/admin/remove-sessions", map[string]any{}, nil); err == nil {
		t.Fatal("client accessed admin mutation")
	}
}
