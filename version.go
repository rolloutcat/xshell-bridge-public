package xshellbridge

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var embeddedVersion string

// Version is the version bundled into this executable, not a runtime file.
var Version = strings.TrimSpace(embeddedVersion)
