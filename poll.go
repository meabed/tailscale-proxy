package main

import (
	"context"
	"log"
	"time"
)

// poll refreshes the store on an interval until ctx is cancelled, doing one
// immediate refresh first. Added/removed routes are logged.
func poll(ctx context.Context, store *RouteStore, interval time.Duration) {
	refresh := func() {
		added, removed, err := store.refresh()
		if err != nil {
			log.Printf("warn: reading routes failed: %v", err)
			return
		}
		for _, h := range added {
			log.Printf("route + %s", h)
		}
		for _, h := range removed {
			log.Printf("route - %s", h)
		}
	}
	refresh()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}
