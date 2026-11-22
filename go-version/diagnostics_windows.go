package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"syscall"
	"time"
)

// Each launch has its own file, including fatal runtime failures from worker
// goroutines. A new launch must never truncate the evidence from a previous one.
func startDiagnostics() func() {
	dir := "logs"
	if e := os.MkdirAll(dir, 0755); e != nil {
		if base, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(base, "WebcamGo", "logs")
			_ = os.MkdirAll(dir, 0755)
		}
	}
	name := filepath.Join(dir, fmt.Sprintf("session-%s-%d.log", time.Now().Format("20060102-150405.000"), os.Getpid()))
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return func() {}
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	debug.SetTraceback("all")
	if e := debug.SetCrashOutput(f, debug.CrashOptions{}); e != nil {
		log.Printf("crash output setup: %v", e)
	}
	log.Printf("START version=%q go=%s pid=%d", version, runtime.Version(), os.Getpid())
	// Keep recent sessions; do not touch unrelated files or the active file.
	if files, e := filepath.Glob(filepath.Join(dir, "session-*.log")); e == nil {
		sort.Strings(files)
		for len(files) > 20 {
			if files[0] != name {
				_ = os.Remove(files[0])
			}
			files = files[1:]
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logHealth()
			}
		}
	}()
	return func() {
		cancel()
		log.Printf("CLEAN EXIT")
		_ = f.Sync()
		// The runtime owns a duplicate for any late fatal error during shutdown.
	}
}

func logHealth() {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	handle, _ := syscall.GetCurrentProcess()
	gui := syscall.NewLazyDLL("user32.dll").NewProc("GetGuiResources")
	gdi, _, _ := gui.Call(uintptr(handle), 0)
	user, _, _ := gui.Call(uintptr(handle), 1)
	log.Printf("HEALTH goroutines=%d heap_mb=%.1f heap_sys_mb=%.1f gc=%d gdi=%d user=%d", runtime.NumGoroutine(), float64(stats.HeapAlloc)/(1<<20), float64(stats.HeapSys)/(1<<20), stats.NumGC, gdi, user)
}
