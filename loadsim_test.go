package loadsim_test

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/metcalf/loadsim"
)

type DummyResource string

func (r DummyResource) Name() string { return string(r) }
func (r DummyResource) Ask(int) int  { return 1 }
func (r DummyResource) Reset()       {}

type DummyMapper []loadsim.ResourceNeed

func (m DummyMapper) RequestNeeds(req *http.Request) ([]*loadsim.ResourceNeed, error) {
	needs := make([]*loadsim.ResourceNeed, len(m))
	for i, need := range m {
		needCopy := need
		needs[i] = &needCopy
	}

	return needs, nil
}

func TestHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		v := r.FormValue("a")
		switch v {
		case "1":
			w.WriteHeader(1)
		case "2":
			w.WriteHeader(2)
		default:
			t.Fatalf("unexpected or missing value for GET parameter `a`: %q", v)
		}
	}))

	req1, err := http.NewRequest("GET", server.URL+"?a=1", nil)
	if err != nil {
		log.Fatal(err)
	}
	req2, err := http.NewRequest("GET", server.URL+"?a=2", nil)
	if err != nil {
		log.Fatal(err)
	}

	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: req1,
			Clock:       &loadsim.WallClock{},
			ID:          "repeat",
		},
		&loadsim.IntervalAgent{
			BaseRequest: req2,
			Clock:       &loadsim.WallClock{},
			Interval:    time.Millisecond * 51,
			ID:          "interval",
		},
	}

	expectations := map[string]struct {
		Code, Count int
	}{
		"repeat":   {1, 3},
		"interval": {2, 6},
	}
	counts := make(map[string]int, len(agents))
	durations := make(map[string][]time.Duration, len(agents))

	worker := &loadsim.HTTPWorker{
		Clock: &loadsim.WallClock{},
	}

	results := loadsim.Simulate(agents, worker, &loadsim.WallClock{}, 290*time.Millisecond)
	for res := range results {
		id := res.AgentID
		if want, have := expectations[id].Code, res.StatusCode; want != have {
			t.Errorf("%s: expected status %d, got %d", id, want, have)
		}
		counts[id]++
		durations[id] = append(durations[id], res.End.Sub(res.Start))
	}

	for id, have := range counts {
		want := expectations[id].Count

		if have != want {
			t.Errorf("%s: expected %d requests, got %d (%v)", id, want, have, durations[id])
		}
	}
}

func TestRepeatSim(t *testing.T) {
	var sim loadsim.SimClock

	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
		},
	}

	worker := makeSimWorker(&sim)

	resCh := loadsim.Simulate(agents, worker, sim.Clock(), time.Second)
	sim.Run(make(chan struct{}))

	results := collectResults(resCh)[""]

	if err := assertStatus(results, http.StatusOK); err != nil {
		t.Error(err)
	}
	if err := assertWorkDurations(results, 10*time.Millisecond, time.Millisecond); err != nil {
		t.Error(err)
	}

	count := len(results)
	if count < 97 || count > 100 {
		t.Errorf("expected 97-100 requests, got %d", count)
	}
}

func TestIntervalSim(t *testing.T) {
	var sim loadsim.SimClock

	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	agents := []loadsim.Agent{
		&loadsim.IntervalAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
			Interval:    51 * time.Millisecond,
		},
	}

	worker := makeSimWorker(&sim)

	resCh := loadsim.Simulate(agents, worker, sim.Clock(), time.Second)
	sim.Run(make(chan struct{}))

	results := collectResults(resCh)[""]

	if err := assertStatus(results, http.StatusOK); err != nil {
		t.Error(err)
	}
	if err := assertWorkDurations(results, 10*time.Millisecond, time.Millisecond); err != nil {
		t.Error(err)
	}

	count := len(results)
	if count != 20 {
		t.Errorf("expected 20 requests, got %d", count)
	}
}

func TestPoolSim(t *testing.T) {
	var sim loadsim.SimClock

	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
			ID:          "repeat1",
		},
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
			ID:          "repeat2",
		},
		&loadsim.IntervalAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
			Interval:    51 * time.Millisecond,
			ID:          "interval",
		},
	}

	worker := &loadsim.WorkerPool{
		Backlog: 10,
		Timeout: 20 * time.Millisecond,
		Workers: []loadsim.Worker{makeSimWorker(&sim), makeSimWorker(&sim)},
		Clock:   sim.Clock(),
	}

	resCh := loadsim.Simulate(agents, worker, sim.Clock(), time.Second)
	sim.Run(make(chan struct{}))

	for id, results := range collectResults(resCh) {
		if err := assertStatus(results, http.StatusOK); err != nil {
			t.Errorf("%s: %s", id, err)
		}
		if err := assertWorkDurations(results, 10*time.Millisecond, time.Millisecond); err != nil {
			t.Errorf("%s: %s", id, err)
		}

		count := len(results)
		switch id {
		case "repeat1", "repeat2":
			if count < 80 || count > 90 {
				t.Errorf("%s: expected 80-90 requests, got %d", id, count)
			}
		case "interval":
			if count != 20 {
				t.Errorf("%s: expected 20 requests, got %d", id, count)
			}
		}
	}
}

func makeSimWorker(sim *loadsim.SimClock) *loadsim.SimWorker {
	return &loadsim.SimWorker{
		ResourceMap: DummyMapper([]loadsim.ResourceNeed{
			{"a", 9},
			{"b", 1},
		}),
		Resources: []loadsim.Resource{
			DummyResource("a"),
			DummyResource("b"),
		},
		Clock: sim.Clock(),
	}
}

func collectResults(resCh <-chan loadsim.Result) map[string][]loadsim.Result {
	all := make(map[string][]loadsim.Result)
	for res := range resCh {
		all[res.AgentID] = append(all[res.AgentID], res)
	}

	return all
}

func assertStatus(results []loadsim.Result, expect int) error {
	codes := make([]int, len(results))
	for i, res := range results {
		codes[i] = res.StatusCode
	}
	for i, code := range codes {
		if code != expect {
			return fmt.Errorf("%d: expected %d, got %d (%v)", i, expect, code, codes)
		}
	}

	return nil
}

func assertWorkDurations(results []loadsim.Result, expect, delta time.Duration) error {
	for i, res := range results {
		dur := res.End.Sub(res.WorkStart)
		diff := expect - dur
		if diff < 0 {
			diff = -diff
		}

		if diff > delta {
			return fmt.Errorf("%d: expected %s, got %s", i, expect, dur)
		}
	}

	return nil
}
