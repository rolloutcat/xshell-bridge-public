package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// These tests change process globals and must not run in parallel.
func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--config", "unused.json", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			oldArgs, oldOut, oldErr := os.Args, os.Stdout, os.Stderr
			out, err := os.CreateTemp(t.TempDir(), "stdout")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			stderr, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			defer stderr.Close()
			defer func() { os.Args, os.Stdout, os.Stderr = oldArgs, oldOut, oldErr }()
			os.Args = append([]string{"xshctl"}, args...)
			os.Stdout, os.Stderr = out, stderr
			code, runErr := run()
			if code != 0 || runErr != nil {
				t.Fatalf("help: code=%d error=%v", code, runErr)
			}
			if _, err = out.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			text, err := io.ReadAll(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(text), usage) {
				t.Fatal("missing usage")
			}
			info, err := stderr.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != 0 {
				t.Fatal("help wrote an error")
			}
		})
	}
}
