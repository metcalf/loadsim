package loadsim_test

import (
	"errors"
	"testing"
	"time"

	"github.com/metcalf/loadsim"
)

func TestSimClockSimple(t *testing.T) {
	stop := make(chan struct{})
	var sim loadsim.SimClock

	// Test a simple clock advancing
	clock1 := sim.Clock()
	ticker1 := clock1.Tick()

	sim.Run(stop)

	start, err := timeoutRead(ticker1)
	if err != nil {
		t.Fatal(err)
	}
	next, err := timeoutRead(ticker1)
	if err != nil {
		t.Fatal(err)
	}

	if next.Sub(start) != time.Millisecond {
		t.Fatalf("clock advanced from %s to %s, not 1ms", start, next)
	}

	// Add another clock to the mix and ensure they move together.
	clock2 := sim.Clock()

	// Before starting the other clock, we should we able to read
	// from this one as many times as we want.
	_, err = timeoutRead(ticker1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = timeoutRead(ticker1)
	if err != nil {
		t.Fatal(err)
	}

	ticker2 := clock2.Tick()

	// With the other clock started, we should read an equal number
	// of ticks from both clocks but we can't be sure which we started
	// from.

	var now1, now2 time.Time
	var count1, count2 int
	for i := 0; i < 10; i++ {
		select {
		case next := <-ticker1:
			if !(now1.IsZero() || next.Sub(now1) == time.Millisecond) {
				t.Fatalf("clock1 advanced from %s to %s, not 1ms", now1, next)
			}
			now1 = next
			count1++
		case next := <-ticker2:
			if !(now2.IsZero() || next.Sub(now2) == time.Millisecond) {
				t.Fatalf("clock2 advanced from %s to %s, not 1ms", now1, next)
			}
			now2 = next
			count2++
		case <-time.After(time.Millisecond):
			t.Fatalf("failed to read from ticker")
		}
	}

	if count1 != count2 {
		t.Fatalf("unequal clock advances, %d != %d", count1, count2)
	}

	if diff := now1.Sub(now2); diff > time.Millisecond || diff < -time.Millisecond {
		t.Fatalf("clocks diverged, %s !~ %s", now1, now2)
	}

	// Ensure we're blocking on ticker1
	select {
	case <-ticker2:
	default:
	}
	select {
	case <-ticker2:
		t.Fatalf("should be blocking on ticker1")
	default:
	}

	// Stop ticker1 and ensure this unblocks us
	clock1.Done()
	_, err = timeoutRead(ticker2)
	if err != nil {
		t.Fatalf("should have unblocked ticker2")
	}
}

func timeoutRead(ticker <-chan time.Time) (time.Time, error) {
	select {
	case now := <-ticker:
		return now, nil
	case <-time.After(time.Millisecond):
		return time.Time{}, errors.New("failed to read from ticker")
	}
}