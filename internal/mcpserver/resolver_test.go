package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

func TestResolveSessionFreshReadOnly(t *testing.T) {
	name, generation, calls := "old", "g1", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/sessions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "read only", 405)
			return
		}
		_ = json.NewEncoder(w).Encode([]bridge.Session{{ID: "id", Generation: generation, Name: name, State: "active"}})
	}))
	defer srv.Close()
	c := &client.Client{Config: client.Config{Endpoint: srv.URL}, HTTP: srv.Client()}
	check := func(query, status, gen string) {
		t.Helper()
		res, _, err := resolveSession(context.Background(), c, ResolveSessionInput{Name: query})
		if err != nil || res.IsError {
			t.Fatalf("%+v %v", res, err)
		}
		var out client.SessionResolution
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		if out.Status != status {
			t.Fatalf("%+v", out)
		}
		if gen != "" && (len(out.Candidates) != 1 || out.Candidates[0].Generation != gen) {
			t.Fatalf("%+v", out)
		}
	}
	check("old", "matched", "g1")
	name, generation = "new", "g2"
	check("new", "matched", "g2")
	check("old", "not_found", "")
	resFuzzy, _, errFuzzy := resolveSession(context.Background(), c, ResolveSessionInput{Name: "NE", Fuzzy: true})
	if errFuzzy != nil || resFuzzy.IsError {
		t.Fatalf("%+v %v", resFuzzy, errFuzzy)
	}
	var suggestion client.SessionResolution
	if err := json.Unmarshal([]byte(resFuzzy.Content[0].(*mcp.TextContent).Text), &suggestion); err != nil {
		t.Fatal(err)
	}
	if suggestion.Status != "suggestions" || suggestion.MatchKind != "substring" || !suggestion.RequiresSelection || len(suggestion.Candidates) != 1 || suggestion.Candidates[0].Generation != "g2" || suggestion.Candidates[0].Name != "new" {
		t.Fatalf("%+v", suggestion)
	}
	if calls != 4 {
		t.Fatalf("expected one fresh GET per resolution including fuzzy fallback, got %d", calls)
	}
	res, _, err := resolveSession(context.Background(), c, ResolveSessionInput{Name: ""})
	if err != nil || !res.IsError || calls != 4 {
		t.Fatal("invalid input should fail without HTTP")
	}
}
