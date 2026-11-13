package bridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeniedPOSTReturnsStructuredErrorOverTCP(t *testing.T) {
	s, _ := setup(t)
	server := httptest.NewServer(Handler(s, Keys{Client: "client-token", Agent: "agent-token", Admin: "admin-token"}))
	defer server.Close()
	r, _ := http.NewRequest("POST", server.URL+"/v1/admin/decide", strings.NewReader(`{"id":"fixture","allow":true}`))
	r.Header.Set("Authorization", "Bearer client-token")
	r.Close = true
	response, e := server.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
}

func TestHTTPRolesAndBrowserOrigin(t *testing.T) {
	s, _ := setup(t)
	h := Handler(s, Keys{Client: "client-token", Agent: "agent-token", Admin: "admin-token"})
	for _, tc := range []struct {
		path, token, origin string
		want                int
	}{
		{"/v1/sessions", "", "", 401},
		{"/v1/sessions", "client-token", "", 200},
		{"/v1/sessions", "agent-token", "", 403},
		{"/v1/admin/decide", "client-token", "", 403},
		{"/v1/admin/decide", "agent-token", "", 403},
		{"/v1/admin/revoke-grant", "client-token", "", 403},
		{"/v1/admin/revoke-grant", "agent-token", "", 403},
		{"/v1/grants", "agent-token", "", 403},
		{"/v1/sessions", "client-token", "https://evil.example", 403},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s got %d want %d", tc.path, tc.token, w.Code, tc.want)
		}
	}
}
func TestStrictInputAndTokenNotInErrors(t *testing.T) {
	s, _ := setup(t)
	h := Handler(s, Keys{Client: "secret-client", Agent: "secret-agent", Admin: "secret-admin"})
	r := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(`{"unexpected":true}`))
	r.Header.Set("Authorization", "Bearer secret-client")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), "secret-") {
		t.Fatal(w.Code, w.Body.String())
	}
}
