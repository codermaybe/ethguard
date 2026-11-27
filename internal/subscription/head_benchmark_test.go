package subscription

import (
	"context"
	"testing"
	"time"
)

func BenchmarkHeadTrackerAdvance(b *testing.B) {
	for _, scenario := range []string{"three_sources", "slow_source", "window_one", "backlog_32"} {
		b.Run(scenario, func(b *testing.B) {
			retained := 16
			if scenario == "window_one" {
				retained = 1
			}
			tr, q, budget := newTracker(b, retained)
			n := chainNode(1, 1, AnchorNode{})
			observeTip(b, tr, "c", 1, n)
			var sequence, peak uint64
			queued := 0
			b.ReportAllocs()
			for b.Loop() {
				sequence++
				n = chainNode(sequence+1, 1, n)
				observeTip(b, tr, "a", sequence, n)
				observeTip(b, tr, "b", sequence, n)
				if scenario == "three_sources" {
					observeTip(b, tr, "c", sequence+1, n)
				}
				p, r, err := tr.Prepare(time.Unix(1, 0))
				if err != nil || p == nil || r != nil {
					b.Fatalf("prepare %v %v %v", p, r, err)
				}
				if err := tr.Publish(q, p); err != nil {
					b.Fatal(err)
				}
				queued++
				if budget.Used() > peak {
					peak = budget.Used()
				}
				if scenario != "backlog_32" || queued == 32 {
					for queued > 0 {
						if _, err := q.Next(context.Background()); err != nil {
							b.Fatal(err)
						}
						queued--
					}
				}
			}
			b.ReportMetric(float64(peak), "retained_peak_B")
		})
	}
}

func BenchmarkHeadTrackerDuplicate(b *testing.B) {
	tr, q, budget := newTracker(b, 16)
	n := chainNode(100, 1, AnchorNode{})
	observeTip(b, tr, "a", 1, n)
	observeTip(b, tr, "b", 1, n)
	commitReady(b, tr, q)
	sequence := uint64(1)
	b.ReportAllocs()
	for b.Loop() {
		sequence++
		observeTip(b, tr, "a", sequence, n)
		p, r, err := tr.Prepare(time.Unix(1, 0))
		if err != nil || p != nil || r != nil {
			b.Fatalf("duplicate produced work: %v %v %v", p, r, err)
		}
	}
	b.ReportMetric(float64(budget.Used()), "retained_B")
}

func BenchmarkHeadTrackerJumpEight(b *testing.B) {
	tr, q, budget := newTracker(b, 16)
	n := chainNode(100, 1, AnchorNode{})
	observeTip(b, tr, "a", 1, n)
	observeTip(b, tr, "b", 1, n)
	commitReady(b, tr, q)
	var peak uint64
	sequence := uint64(1)
	b.ReportAllocs()
	for b.Loop() {
		sequence++
		objects := make(map[[32]byte]AnchorNode, 8)
		for range 8 {
			n = chainNode(n.Number+1, 1, n)
			objects[n.Hash] = n
		}
		observeTip(b, tr, "a", sequence, n)
		observeTip(b, tr, "b", sequence, n)
		for {
			p, r, err := tr.Prepare(time.Unix(1, 0))
			if err != nil {
				b.Fatal(err)
			}
			if budget.Used() > peak {
				peak = budget.Used()
			}
			if p != nil {
				if err := tr.Publish(q, p); err != nil {
					b.Fatal(err)
				}
				if _, err := q.Next(context.Background()); err != nil {
					b.Fatal(err)
				}
				break
			}
			if r == nil {
				b.Fatal("jump stopped")
			}
			supplyObject(b, tr, r, objects[r.Hash])
		}
	}
	b.ReportMetric(float64(peak), "reserved_peak_B")
}
