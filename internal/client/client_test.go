package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectUnsafeEndpoints(t *testing.T) {
	for _, endpoint := range []string{"http://192.0.2.1:18765", "http://localhost:18765", "http://user:pass@127.0.0.1:18765", "file:///tmp/config", "http://127.0.0.1:18765?token=bad"} {
		if _, e := New(Config{endpoint, strings.Repeat("x", 48)}); e == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestRedirectDoesNotForwardBearer(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	c, e := New(Config{source.URL, strings.Repeat("x", 48)})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Call(context.Background(), "GET", "/v1/sessions", nil, nil); e == nil {
		t.Fatal("redirect accepted")
	}
	if reached {
		t.Fatal("bearer request followed redirect")
	}
}
