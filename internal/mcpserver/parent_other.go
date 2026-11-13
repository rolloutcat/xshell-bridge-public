//go:build !windows

package mcpserver

import "context"

func parentContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
