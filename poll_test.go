package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPoll_picksUpChanges(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "routes.json")
	os.WriteFile(p, []byte(`[]`), 0o644)
	store := NewRouteStore(p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poll(ctx, store, 10*time.Millisecond)

	os.WriteFile(p, []byte(`[{"hostname":"x.local","port":9}]`), 0o644)

	deadline := time.After(2 * time.Second)
	for {
		if port, ok := store.lookup("x.local"); ok && port == 9 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("poll did not pick up the new route")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
