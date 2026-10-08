package core

import (
	"context"
	"github.com/ucgeorge/switchboard/internal/openai"
	"sync"
	"testing"
	"time"
)

func TestConcurrentClaimsRespectChannelCapacity(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	key := mustKey(t, svc)
	ch := mustChannel(t, svc, "concurrent")
	for range 8 {
		submit(t, svc, key, "m", msg("user", "work"))
	}
	var wg sync.WaitGroup
	wg.Add(8)
	results := make(chan bool, 8)
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			defer wg.Done()
			r, e := svc.ClaimRequest(ctx, ch.ID, "tok_test", 0)
			if e != nil {
				errs <- e
			}
			results <- r != nil
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	claimed := 0
	for yes := range results {
		if yes {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed %d concurrently, capacity is 1", claimed)
	}
	// Ensure an output still completes normally after contention.
	held, _ := svc.Q.ListInFlightForChannel(ctx, strp(ch.ID))
	if len(held) != 1 {
		t.Fatal(held)
	}
	if _, e := svc.CompleteRequest(ctx, ch.ID, "tok_test", held[0].ID, openai.Answer{Content: "ok"}); e != nil {
		t.Fatal(e)
	}
	if r, e := svc.ClaimRequest(ctx, ch.ID, "tok_test", time.Second); e != nil || r == nil {
		t.Fatalf("capacity not released: %v", e)
	}
}
