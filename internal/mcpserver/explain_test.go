package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

func TestExplainTransport(t *testing.T) {
	calls := 0
	in := bridge.ExplainRequest{SessionID: "s", Generation: "g", Command: "printf hello", Cwd: "/fixture", TimeoutMS: 10000}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.RequestURI() != "/v1/explain" {
			t.Errorf("%s %s", r.Method, r.URL.RequestURI())
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		b, _ := json.Marshal(in)
		var want map[string]any
		json.Unmarshal(b, &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%#v", got)
		}
		w.Write([]byte(`{"decision":"manual","checks":[]}`))
	}))
	defer srv.Close()
	c := &client.Client{Config: client.Config{Endpoint: srv.URL}, HTTP: srv.Client()}
	result, _, err := explainCommand(context.Background(), c, in)
	if err != nil || result.IsError || calls != 1 {
		t.Fatalf("%+v %v %d", result, err, calls)
	}
	var out bridge.ApprovalExplanation
	if json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &out) != nil || out.Decision != "manual" {
		t.Fatal(result)
	}
}
