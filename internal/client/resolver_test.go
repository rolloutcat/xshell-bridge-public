package client

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

func TestResolveSessions(t *testing.T) {
	a := bridge.Session{ID: "a", Generation: "new-generation", Name: "新名称", Environment: "甲", State: "active"}
	b := a
	b.ID, b.Environment = "b", "乙"
	detached := a
	detached.ID, detached.State = "detached", "detached"
	for _, tc := range []struct {
		label             string
		sessions          []bridge.Session
		name, env, status string
		count             int
	}{
		{"rename new", []bridge.Session{a}, " 新名称 ", "", "matched", 1},
		{"rename old", []bridge.Session{a}, "旧名称", "", "not_found", 0},
		{"different groups", []bridge.Session{a, b}, a.Name, "", "ambiguous", 2},
		{"group filter", []bridge.Session{a, b}, a.Name, " 甲 ", "matched", 1},
		{"same group", []bridge.Session{a, a}, a.Name, "甲", "ambiguous", 2},
		{"detached ignored", []bridge.Session{detached, a}, a.Name, "", "matched", 1},
		{"detached only", []bridge.Session{detached}, a.Name, "", "not_found", 0},
		{"exact environment", []bridge.Session{a}, a.Name, "乙", "not_found", 0},
		{"case sensitive", []bridge.Session{{Name: "Name", State: "active"}}, "name", "", "not_found", 0},
		{"unicode exact", []bridge.Session{{Name: "é", State: "active"}}, "e\u0301", "", "not_found", 0},
	} {
		t.Run(tc.label, func(t *testing.T) {
			before := append([]bridge.Session(nil), tc.sessions...)
			got, err := ResolveSessions(tc.sessions, tc.name, tc.env)
			if err != nil || got.Status != tc.status || len(got.Candidates) != tc.count || got.Candidates == nil {
				t.Fatalf("%+v, %v", got, err)
			}
			if !reflect.DeepEqual(before, tc.sessions) {
				t.Fatal("input mutated")
			}
			if got.Status == "matched" && got.Candidates[0].Generation != a.Generation {
				t.Fatal("generation lost")
			}
		})
	}
	for _, pair := range [][2]string{{"", ""}, {"  ", ""}, {"bad\n", ""}, {"ok", "bad\t"}, {strings.Repeat("é", 101), ""}, {"ok", strings.Repeat("a", 201)}, {string([]byte{255}), ""}} {
		if _, err := ResolveSessions(nil, pair[0], pair[1]); err == nil {
			t.Fatalf("accepted invalid input %q", pair)
		}
	}
}
