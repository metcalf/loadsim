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
		},
		&loadsim.IntervalAgent{
			BaseRequest: req2,
			Clock:       &loadsim.WallClock{},
			Interval:    time.Millisecond * 51,
		},
	}

	expectations := []struct {
		Code, Count int
	}{
		{1, 3},
		{2, 6},
	}
	counts := make(map[int]int, len(agents))
	durations := make(map[int][]time.Duration, len(agents))

	worker := &loadsim.HTTPWorker{
		Clock: &loadsim.WallClock{},
	}

	results := loadsim.Simulate(agents, worker, &loadsim.WallClock{}, 290*time.Millisecond)
	for ares := range results {
		for i, expect := range expectations {
			if ares.Agent != agents[i] {
				continue
			}

			res := ares.Result
			if want, have := expect.Code, res.StatusCode; want != have {
				t.Errorf("%d: expected status %d, got %d", i, want, have)
			}
			counts[i]++
			durations[i] = append(durations[i], res.End.Sub(res.Start))
			break
		}
	}

	for i, have := range counts {
		want := expectations[i].Count
		//diff := want - have

		if have != want {
			t.Errorf("%d: expected %d requests, got %d (%v)", i, want, have, durations[i])
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

	results := collectResults(resCh)[agents[0]]

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

	results := collectResults(resCh)[agents[0]]

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
		},
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
		},
		&loadsim.IntervalAgent{
			BaseRequest: req,
			Clock:       sim.Clock(),
			Interval:    51 * time.Millisecond,
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

	for agent, results := range collectResults(resCh) {
		if err := assertStatus(results, http.StatusOK); err != nil {
			t.Errorf("%s: %s", agent, err)
		}
		if err := assertWorkDurations(results, 10*time.Millisecond, time.Millisecond); err != nil {
			t.Errorf("%s: %s", agent, err)
		}

		count := len(results)
		switch agent.(type) {
		case *loadsim.RepeatAgent:
			if count < 80 || count > 90 {
				t.Errorf("expected 80-90 requests, got %d", count)
			}
		case *loadsim.IntervalAgent:
			if count != 20 {
				t.Errorf("%s: expected 20 requests, got %d", agent, count)
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

func collectResults(resCh <-chan loadsim.AgentResult) map[loadsim.Agent][]loadsim.Result {
	all := make(map[loadsim.Agent][]loadsim.Result)
	for ares := range resCh {
		all[ares.Agent] = append(all[ares.Agent], ares.Result)
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
