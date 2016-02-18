package loadsim_test

import (
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

	agent := &loadsim.RepeatAgent{
		BaseRequest: req,
		Clock:       sim.Clock(),
	}

	count := runSim(t, sim, agent)

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

	agent := &loadsim.IntervalAgent{
		BaseRequest: req,
		Clock:       sim.Clock(),
		Interval:    51 * time.Millisecond,
	}

	count := runSim(t, sim, agent)

	if count != 20 {
		t.Errorf("expected 20 requests, got %d", count)
	}
}

func runSim(t *testing.T, sim loadsim.SimClock, agent loadsim.Agent) int {
	agents := []loadsim.Agent{agent}

	worker := &loadsim.SimWorker{
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

	results := loadsim.Simulate(agents, worker, sim.Clock(), time.Second)

	stop := make(chan struct{})
	sim.Run(stop)

	var count int
	var durations []time.Duration
	for ares := range results {
		res := ares.Result
		if want, have := 200, res.StatusCode; want != have {
			t.Errorf("%s: expected status %d, got %d", agent, want, have)
		}
		count++
		durations = append(durations, res.End.Sub(res.Start))
	}

	close(stop)

	for i, d := range durations {
		diff := 10*time.Millisecond - d
		if diff > time.Millisecond {
			t.Errorf("%s %d: expected 10ms, got %s (%v)", agent, i, d, durations)
		}
	}

	t.Logf("%s: %v", agent, durations)

	return count
}
