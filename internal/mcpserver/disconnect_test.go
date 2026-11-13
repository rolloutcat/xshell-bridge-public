package mcpserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

func TestDisconnectCancelsInFlightWait(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
	defer srv.Close()
	c, err := client.New(client.Config{Endpoint: srv.URL, Token: strings.Repeat("t", 32)})
	if err != nil {
		t.Fatal(err)
	}
	serverConn, clientConn := net.Pipe()
	a := &mcp.IOTransport{Reader: serverConn, Writer: serverConn}
	b := &mcp.IOTransport{Reader: clientConn, Writer: clientConn}
	ss, err := New(c).Connect(context.Background(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil).Connect(context.Background(), b, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wait_job", Arguments: WaitInput{ID: "fixture"}})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not reach fixture")
	}
	_ = clientConn.Close()
	defer cs.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request was not cancelled on disconnect")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tool wait did not end on disconnect")
	}
}
