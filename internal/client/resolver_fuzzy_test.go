package client

import (
	"reflect"
	"testing"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

func TestResolveDiscovery(t *testing.T) {
	active := func(id, name, env string) bridge.Session {
		return bridge.Session{ID: id, Generation: "g-" + id, Name: name, Environment: env, State: "active"}
	}
	a := active("a", "Prod", "Group")
	partial := active("b", "Production", "Group")
	detached := active("d", "Prod", "Group")
	detached.State = "detached"
	deleted := active("x", "Production", "Group")
	deleted.State = "deleted"
	for _, tc := range []struct {
		label        string
		sessions     []bridge.Session
		name, env    string
		fuzzy        bool
		status, kind string
		ids          []string
	}{
		{"exact priority", []bridge.Session{partial, a}, "Prod", "", true, "matched", "exact", []string{"a"}},
		{"duplicate exact", []bridge.Session{a, a, partial}, "Prod", "", true, "ambiguous", "exact", []string{"a", "a"}},
		{"single suggestion", []bridge.Session{partial}, "prod", "", true, "suggestions", "substring", []string{"b"}},
		{"casefold trim", []bridge.Session{active("c", "  PRODUCTION  ", "Group")}, " pRoD ", "", true, "suggestions", "substring", []string{"c"}},
		{"Chinese", []bridge.Session{active("c", "生产环境", "甲")}, "生产", "甲", true, "suggestions", "substring", []string{"c"}},
		{"old name no alias", []bridge.Session{a}, "old-prod", "", true, "not_found", "none", nil},
		{"literal old substring", []bridge.Session{active("o", "old-prod-new", "")}, "old-prod", "", true, "suggestions", "substring", []string{"o"}},
		{"short ascii", []bridge.Session{partial}, "P", "", true, "not_found", "none", nil},
		{"short rune", []bridge.Session{active("c", "终端", "")}, "终", "", true, "not_found", "none", nil},
		{"short exact allowed", []bridge.Session{active("c", "终", "")}, "终", "", true, "matched", "exact", []string{"c"}},
		{"environment not substring", []bridge.Session{partial}, "prod", "Grou", true, "not_found", "none", nil},
		{"environment not casefold", []bridge.Session{partial}, "prod", "group", true, "not_found", "none", nil},
		{"inactive excluded", []bridge.Session{detached, deleted}, "Prod", "", true, "not_found", "none", nil},
		{"disabled", []bridge.Session{partial}, "prod", "", false, "not_found", "none", nil},
	} {
		t.Run(tc.label, func(t *testing.T) {
			before := append([]bridge.Session(nil), tc.sessions...)
			got, err := ResolveSessionsWithOptions(tc.sessions, tc.name, tc.env, tc.fuzzy)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, s := range got.Candidates {
				ids = append(ids, s.ID)
				if s.Generation != "g-"+s.ID || s.State != "active" {
					t.Fatalf("lost full active metadata: %+v", s)
				}
			}
			if got.Status != tc.status || got.MatchKind != tc.kind || got.RequiresSelection != (tc.status != "matched") || !reflect.DeepEqual(ids, tc.ids) || got.Candidates == nil {
				t.Fatalf("unexpected resolution: %+v", got)
			}
			if !reflect.DeepEqual(before, tc.sessions) {
				t.Fatal("input mutated")
			}
		})
	}
}
