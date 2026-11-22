package main

import (
	"sync"
	"testing"
)

func TestNativeEnumerationSurvivesThousandsOfFrames(t *testing.T) {
	if len(monitors()) == 0 {
		t.Skip("requires an interactive Windows desktop")
	}
	// The old implementation exhausted Go's native callback table before this
	// completed. Exercise actual Windows APIs, including simultaneous requests.
	var workers sync.WaitGroup
	for n := 0; n < 4; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 2000; i++ {
				if len(monitors()) == 0 {
					t.Error("monitor result lost during concurrent enumeration")
					return
				}
				if i%4 == 0 {
					_ = windowDevices()
				}
			}
		}()
	}
	workers.Wait()
	count := 0
	enumerationRequests.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Fatalf("completed native enumerations retained: %d", count)
	}
}
