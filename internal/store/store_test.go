package store

import (
	"context"
	"testing"
	"time"
)

func TestReopenKeepsBlockState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	since := time.Unix(1_700_000_000, 0)
	if err := s.SaveNodeStates(ctx, map[string]*NodeState{"n": {UUID: "n", Name: "n", Status: "up", DropSince: &since, BlockedSince: &since, Usual: 42}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOnlineSamples(ctx, since, map[string]int{"n": 7}, since.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// The columns added by the migration already exist now.
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	states, err := s.NodeStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n := states["n"]; n == nil || n.Usual != 42 || n.BlockedSince == nil || !n.BlockedSince.Equal(since) || n.RiseSince != nil {
		t.Fatalf("block state not kept: %+v", n)
	}
	samples, err := s.OnlineSamples(ctx, since.Add(-time.Minute))
	if err != nil || len(samples["n"]) != 1 || samples["n"][0].Online != 7 {
		t.Fatalf("unexpected samples: %+v %v", samples, err)
	}
	// Older history is dropped on the next write.
	if err := s.AddOnlineSamples(ctx, since.Add(2*time.Hour), map[string]int{"n": 9}, since.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if samples, _ := s.OnlineSamples(ctx, time.Unix(0, 0)); len(samples["n"]) != 1 || samples["n"][0].Online != 9 {
		t.Fatalf("old history not pruned: %+v", samples)
	}
}
