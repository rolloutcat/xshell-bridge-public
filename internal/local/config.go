package local

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

type Config struct {
	Listen string      `json:"listen"`
	Keys   bridge.Keys `json:"keys"`
}

func WriteJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
func Init(dir string) error {
	files, e := os.ReadDir(dir)
	if e == nil && len(files) > 0 {
		return fmt.Errorf("init requires an empty directory")
	}
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = Protect(dir); e != nil {
		return e
	}
	k := bridge.Keys{Client: bridge.ID() + bridge.ID(), Agent: bridge.ID() + bridge.ID(), Admin: bridge.ID() + bridge.ID()}
	values := map[string]any{"bridge.json": Config{"127.0.0.1:18765", k}, "client.json": client.Config{Endpoint: "http://127.0.0.1:18765", Token: k.Client}, "admin.json": client.Config{Endpoint: "http://127.0.0.1:18765", Token: k.Admin}, "agent.json": map[string]any{"endpoint": "http://127.0.0.1:18765", "token": k.Agent, "binding_mode": "manual_terminal", "environment": "默认分组"}}
	for name, v := range values {
		if e = WriteJSON(filepath.Join(dir, name), v); e != nil {
			return e
		}
	}
	return nil
}
func Load(dir string) (Config, error) {
	var c Config
	b, e := os.ReadFile(filepath.Join(dir, "bridge.json"))
	if e == nil {
		e = json.Unmarshal(b, &c)
	}
	if e == nil && (len(c.Keys.Client) < 32 || len(c.Keys.Agent) < 32 || len(c.Keys.Admin) < 32 || c.Keys.Client == c.Keys.Agent || c.Keys.Client == c.Keys.Admin || c.Keys.Agent == c.Keys.Admin) {
		e = fmt.Errorf("invalid or overlapping role tokens")
	}
	return c, e
}
