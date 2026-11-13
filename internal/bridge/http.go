package bridge

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	xshellbridge "github.com/rolloutcat/xshell-bridge-public"
)

type Keys struct {
	Client string `json:"client"`
	Agent  string `json:"agent"`
	Admin  string `json:"admin"`
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, err error) {
	e := &Error{Code: "INTERNAL_ERROR", Message: "operation failed"}
	_ = errors.As(err, &e)
	status := 400
	switch e.Code {
	case "UNAUTHORIZED":
		status = 401
	case "FORBIDDEN", "COMMAND_DENIED":
		status = 403
	case "NOT_FOUND":
		status = 404
	case "CONFLICT", "SESSION_CHANGED", "NEEDS_AUTHORIZATION":
		status = 409
	case "STORAGE_ERROR", "INTERNAL_ERROR":
		status = 500
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Method != "POST" {
		return fail("INVALID_ARGUMENT", "POST required")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return fail("INVALID_ARGUMENT", "invalid JSON request")
	}
	if d.Decode(new(any)) != io.EOF {
		return fail("INVALID_ARGUMENT", "expected one JSON object")
	}
	return nil
}
func Handler(s *Store, keys Keys) http.Handler {
	return HandlerWithShutdown(s, keys, nil)
}

func HandlerWithShutdown(s *Store, keys Keys, shutdown func()) http.Handler {
	return HandlerWithMCP(s, keys, shutdown, nil)
}

// MCP enters through the same origin and bearer-role checks as the client API.
func HandlerWithMCP(s *Store, keys Keys, shutdown func(), mcpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// urllib may send headers and body separately with Connection: close.
		// Drain a bounded body on early rejection so Windows returns JSON, not TCP RST.
		r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
		defer io.Copy(io.Discard, r.Body)
		if r.Header.Get("Origin") != "" {
			problem(w, fail("FORBIDDEN", "browser origins are not accepted"))
			return
		}
		role := ""
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		for name, key := range map[string]string{"client": keys.Client, "agent": keys.Agent, "admin": keys.Admin} {
			if key != "" && subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
				role = name
			}
		}
		source := ""
		if role == "" {
			if authenticated, ok := authenticateSource(keys.Agent, token); ok {
				role, source = "agent", authenticated
			}
		}
		if role == "" {
			problem(w, fail("UNAUTHORIZED", "valid bearer token required"))
			return
		}
		needed := "client"
		if strings.HasPrefix(r.URL.Path, "/v1/agent/") {
			needed = "agent"
		}
		if strings.HasPrefix(r.URL.Path, "/v1/admin/") {
			needed = "admin"
		}
		if role != needed && !(role == "admin" && needed == "client") {
			problem(w, fail("FORBIDDEN", "token role cannot use this endpoint"))
			return
		}
		if r.URL.Path == "/mcp" && mcpHandler != nil {
			mcpHandler.ServeHTTP(w, r)
			return
		}
		var result any = map[string]bool{"ok": true}
		var err error
		switch r.URL.Path {
		case "/v1/health":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			result = map[string]any{"version": xshellbridge.Version, "protocol": 1, "approval": "exact-command", "temporary_grants": true, "builtin_policy": true, "displayed_approval": true, "approval_rules": true, "approval_explain": true, "batch_approval": true, "session_labels": true, "session_removal": true, "source_agents": true, "output_fidelity": "terminal-text"}
			result.(map[string]any)["mcp_http"] = mcpHandler != nil
			result.(map[string]any)["bridge_pid"] = os.Getpid()
			if shutdown != nil {
				result.(map[string]any)["safe_shutdown"] = true
				result.(map[string]any)["desktop_shutdown"] = true
			}
		case "/v1/explain":
			var v ExplainRequest
			if err = decode(w, r, &v); err == nil {
				result, err = s.Explain(v)
			}
		case "/v1/policy":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
			} else {
				result = s.BuiltinPolicy()
			}
		case "/v1/command-policy":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
			} else {
				result = s.CommandPolicy()
			}
		case "/v1/admin/policy":
			var v struct {
				Expected *BuiltinPolicy `json:"expected"`
				Enabled  *bool          `json:"enabled"`
			}
			if err = decode(w, r, &v); err == nil {
				if v.Expected == nil || v.Enabled == nil {
					err = fail("INVALID_ARGUMENT", "expected policy and explicit enabled required")
				} else {
					err = s.SetBuiltinPolicy(PolicyChange{Expected: *v.Expected, Enabled: *v.Enabled})
				}
			}
		case "/v1/admin/decide-batch":
			var v struct {
				Allow           *bool       `json:"allow"`
				Items           []BatchItem `json:"items"`
				RememberMinutes int         `json:"remember_minutes,omitempty"`
			}
			if err = decode(w, r, &v); err == nil {
				if v.Allow == nil {
					err = fail("INVALID_ARGUMENT", "allow must be an explicit JSON boolean")
				} else {
					err = s.DecideBatch(BatchDecision{Allow: *v.Allow, Items: v.Items, RememberMinutes: v.RememberMinutes})
				}
			}
		case "/v1/rules":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			result = s.Rules()
		case "/v1/admin/rules":
			var v RuleSpec
			if err = decode(w, r, &v); err == nil {
				result, err = s.CreateRule(v)
			}
		case "/v1/admin/revoke-rule":
			var v struct {
				ID string `json:"id"`
			}
			if err = decode(w, r, &v); err == nil {
				err = s.RevokeRule(v.ID)
			}
		case "/v1/admin/shutdown", "/v1/admin/desktop-shutdown":
			err = shutdownRequest(w, r, s, shutdown)
		case "/v1/grants":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			result = s.Grants()
		case "/v1/admin/session-label":
			err = sessionLabelRequest(w, r, s)
		case "/v1/admin/remove-sessions":
			var v struct {
				StopActive *bool         `json:"stop_active"`
				Items      []RemovalItem `json:"items"`
			}
			if err = decode(w, r, &v); err == nil {
				if v.StopActive == nil {
					err = fail("INVALID_ARGUMENT", "stop_active must be an explicit JSON boolean")
				} else {
					err = s.RemoveSessions(*v.StopActive, v.Items)
				}
			}
		case "/v1/admin/sessions":
			if r.Method != "GET" || r.URL.RawQuery != "include_archived=true" {
				err = fail("INVALID_ARGUMENT", "GET with include_archived=true required")
				break
			}
			result = s.sessions(true)
		case "/v1/admin/revoke-grant":
			var v struct {
				ID string `json:"id"`
			}
			if err = decode(w, r, &v); err == nil {
				err = s.RevokeGrant(v.ID)
			}
		case "/v1/sessions":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			result = s.Sessions()
		case "/v1/audit":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			result = s.Audits()
		case "/v1/jobs":
			if r.Method == "GET" {
				jobs := s.Jobs()
				for i := range jobs {
					jobs[i].Output = ""
				}
				result = jobs
			} else {
				var v Submit
				if err = decode(w, r, &v); err == nil {
					result, err = s.Submit(v)
				}
			}
		case "/v1/job":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			var j Job
			j, err = s.GetJob(r.URL.Query().Get("id"))
			if err == nil {
				j.Output = ""
				result = j
			}
		case "/v1/output":
			if r.Method != "GET" {
				err = fail("INVALID_ARGUMENT", "GET required")
				break
			}
			var j Job
			j, err = s.GetJob(r.URL.Query().Get("id"))
			if err != nil {
				break
			}
			cursor := 0
			if v := r.URL.Query().Get("cursor"); v != "" {
				cursor, err = strconv.Atoi(v)
			}
			if err != nil || cursor < 0 || cursor > len(j.Output) || !utf8.ValidString(j.Output[:cursor]) {
				err = fail("INVALID_ARGUMENT", "invalid byte cursor")
				break
			}
			end := cursor + 16384
			if end > len(j.Output) {
				end = len(j.Output)
			}
			for !utf8.ValidString(j.Output[cursor:end]) {
				end--
			}
			result = map[string]any{"id": j.ID, "output": j.Output[cursor:end], "next_cursor": end, "status": j.Status, "exit_code": j.ExitCode, "truncated": j.Truncated}
		case "/v1/interrupt":
			var v struct {
				ID string `json:"id"`
			}
			if err = decode(w, r, &v); err == nil {
				err = s.Interrupt(v.ID)
			}
		case "/v1/detach", "/v1/admin/activate":
			var v struct {
				ID         string `json:"id"`
				Generation string `json:"generation"`
			}
			if err = decode(w, r, &v); err == nil {
				if needed == "admin" {
					err = s.Activate(v.ID, v.Generation)
				} else {
					err = s.Detach(v.ID, v.Generation)
				}
			}
		case "/v1/admin/decide":
			var v struct {
				ID              string `json:"id"`
				Allow           bool   `json:"allow"`
				RememberMinutes int    `json:"remember_minutes"`
			}
			if err = decode(w, r, &v); err == nil {
				err = s.DecideRemember(v.ID, v.Allow, v.RememberMinutes)
			}
		case "/v1/agent/register":
			var v struct {
				AutoName    bool   `json:"auto_name"`
				Instance    string `json:"instance"`
				Name        string `json:"name"`
				Environment string `json:"environment"`
				Fingerprint string `json:"fingerprint"`
			}
			if err = decode(w, r, &v); err == nil {
				if err = checkRegistrationSource(v.Fingerprint, source); err == nil {
					// Reserve a bounded source-specific instance namespace without
					// changing direct Store callers or local instance identity.
					if source != "" {
						if len(v.Instance) < 16 || len(v.Instance) > 128 {
							err = fail("INVALID_ARGUMENT", "invalid registration")
						} else {
							v.Instance = sourceInstance(source, v.Instance)
						}
					}
					if err == nil {
						result, err = s.RegisterNamed(v.Instance, v.Name, v.Environment, v.Fingerprint, v.AutoName)
					}
				}
			}
		case "/v1/agent/poll":
			var v struct {
				ID          string `json:"id"`
				Generation  string `json:"generation"`
				Fingerprint string `json:"fingerprint"`
			}
			if err = decode(w, r, &v); err == nil {
				if err = s.CheckAgentSource(source, v.ID); err == nil {
					result, err = s.Poll(v.ID, v.Generation, v.Fingerprint)
				}
			}
		case "/v1/agent/heartbeat":
			var v struct {
				Members []AgentMember `json:"members"`
			}
			if err = decode(w, r, &v); err == nil {
				ids := make([]string, len(v.Members))
				for i, member := range v.Members {
					ids[i] = member.ID
				}
				if err = s.CheckAgentSource(source, ids...); err == nil {
					err = s.Heartbeat(v.Members)
				}
			}
		case "/v1/agent/detach":
			var v AgentMember
			if err = decode(w, r, &v); err == nil {
				if err = s.CheckAgentSource(source, v.ID); err == nil {
					err = s.AgentDetach(v)
				}
			}
		case "/v1/agent/event":
			var v Event
			if err = decode(w, r, &v); err == nil {
				if err = s.CheckAgentSource(source, v.SessionID); err == nil {
					err = s.Event(v)
				}
			}
		default:
			err = fail("NOT_FOUND", "endpoint not found")
		}
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, result)
		if (r.URL.Path == "/v1/admin/shutdown" || r.URL.Path == "/v1/admin/desktop-shutdown") && shutdown != nil {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			shutdown()
		}
	})
}
