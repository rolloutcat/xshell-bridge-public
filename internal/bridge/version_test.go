package bridge

import (
	"encoding/json"
	xshellbridge "github.com/rolloutcat/xshell-bridge-public"
	"net/http/httptest"
	"testing"
)

func TestHealthBundledVersion(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/health", nil)
	r.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	Handler(nil, Keys{Client: "test"}).ServeHTTP(w, r)
	var v struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || v.Version != xshellbridge.Version || v.Version == "" {
		t.Fatalf("%d %+v", w.Code, v)
	}
	var capabilities map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	if _, present := capabilities["window_controller"]; present {
		t.Fatal("retired window controller must not be advertised")
	}
}
