package xshellbridge

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedVersion(t *testing.T) {
	b, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(b))
	if want == "" || Version != want {
		t.Fatalf("embedded=%q root=%q", Version, want)
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`).MatchString(Version) {
		t.Fatalf("not a semantic version: %q", Version)
	}
}
