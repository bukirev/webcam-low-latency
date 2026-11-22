package main

import (
	"context"
	"time"
)

// Device removal is recoverable. The same session reconnects without requiring
// a new window, while Stop cancels both the stream and this bounded delay.
// retryMedia повторяет нормально вернувшийся worker через отменяемую секунду.
// Он не перехватывает panic/fatal runtime и не является watchdog зависшего
// native вызова; диагностика и корректный ресурсный lifecycle остаются обязательны.
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
