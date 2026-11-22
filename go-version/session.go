package main

import (
	"context"
	"time"
)

// Device removal is recoverable. The same session reconnects without requiring
// a new window, while Stop cancels both the stream and this bounded delay.
func retryMedia(ctx context.Context, run func()) {
	for ctx.Err() == nil {
		run()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
