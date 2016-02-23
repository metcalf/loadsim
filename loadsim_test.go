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

func (m DummyMapper) RequestNeeds(req *http.Request) []*loadsim.ResourceNeed {
	needs := make([]*loadsim.ResourceNeed, len(m))
	for i, need := range m {
		needCopy := need
		needs[i] = &needCopy
	}

	return needs
}

func TestHTTP(t *testing.T) {
	var clk loadsim.WallClock
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
			Clock:       &clk,
			ID:          "repeat",
		},
		&loadsim.IntervalAgent{
			BaseRequest: req2,
			Clock:       &clk,
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
		Clock: &clk,
	}

	results := loadsim.Simulate(agents, worker, &clk, 290*time.Millisecond)
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
	clk := loadsim.NewSimClock()

	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       clk,
		},
	}

	worker := makeSimWorker(clk)

	stop := make(chan struct{})
	resCh := loadsim.Simulate(agents, worker, clk, 10*time.Second)
	go clk.Run(stop)

	results := collectResults(resCh)[""]
	close(stop)

	if err := assertStatus(results, http.StatusOK); err != nil {
		t.Error(err)
	}
	if err := assertWorkDurations(results, 100*time.Millisecond, time.Millisecond); err != nil {
		t.Error(err)
	}

	count := len(results)
	if count < 99 || count > 101 {
		t.Errorf("expected 99-101 requests, got %d", count)
	}
}

func TestIntervalSim(t *testing.T) {
	clk := loadsim.NewSimClock()

	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	agents := []loadsim.Agent{
		&loadsim.IntervalAgent{
			BaseRequest: req,
			Clock:       clk,
			Interval:    510 * time.Millisecond,
		},
	}

	worker := makeSimWorker(clk)

	stop := make(chan struct{})
	resCh := loadsim.Simulate(agents, worker, clk, 10*time.Second)
	go clk.Run(stop)

	results := collectResults(resCh)[""]
	close(stop)

	if err := assertStatus(results, http.StatusOK); err != nil {
		t.Error(err)
	}
	if err := assertWorkDurations(results, 100*time.Millisecond, time.Millisecond); err != nil {
		t.Error(err)
	}

	count := len(results)
	if count != 20 {
		t.Errorf("expected 20 requests, got %d", count)
	}
}

func TestPoolSim(t *testing.T) {
	clk := loadsim.NewSimClock()

	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       clk,
			ID:          "repeat1",
		},
		&loadsim.RepeatAgent{
			BaseRequest: req,
			Clock:       clk,
			ID:          "repeat2",
		},
		&loadsim.IntervalAgent{
			BaseRequest: req,
			Clock:       clk,
			Interval:    510 * time.Millisecond,
			ID:          "interval",
		},
	}

	worker := &loadsim.WorkerPool{
		Backlog: 10,
		Timeout: 200 * time.Millisecond,
		Workers: []loadsim.Worker{makeSimWorker(clk), makeSimWorker(clk)},
		Clock:   clk,
	}

	stop := make(chan struct{})
	resCh := loadsim.Simulate(agents, worker, clk, 10*time.Second)
	go clk.Run(stop)

	agentResults := collectResults(resCh)
	close(stop)

	for id, results := range agentResults {
		if err := assertStatus(results, http.StatusOK); err != nil {
			t.Errorf("%s: %s", id, err)
		}
		if err := assertWorkDurations(results, 100*time.Millisecond, time.Millisecond); err != nil {
			t.Errorf("%s: %s", id, err)
		}

		count := len(results)
		switch id {
		case "repeat1", "repeat2":
			if count < 85 || count > 95 {
				t.Errorf("%s: expected 85-95 requests, got %d", id, count)
			}
		case "interval":
			if count != 20 {
				t.Errorf("%s: expected 20 requests, got %d", id, count)
			}
		}
	}
}

func makeSimWorker(clk loadsim.Clock) *loadsim.SimWorker {
	return &loadsim.SimWorker{
		ResourceMapper: DummyMapper([]loadsim.ResourceNeed{
			{"a", 90},
			{"b", 10},
		}).RequestNeeds,
		Resources: []loadsim.Resource{
			DummyResource("a"),
			DummyResource("b"),
		},
		Clock: clk,
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
