package loadsim

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

type Agent interface {
	Run(chan<- Task, <-chan struct{}) <-chan Result
}

type RepeatAgent struct {
	BaseRequest *http.Request
	Body        []byte
	Clock       interface {
		Now() time.Time
	}
}

func (a *RepeatAgent) Run(queue chan<- Task, stop <-chan struct{}) <-chan Result {
	results := make(chan Result)

	go func() {
		for {
			task := Task{copyRequest(a.BaseRequest, a.Body), make(chan Result, 1)}
			queue <- task

			start := a.Clock.Now()
			res := <-task.Result
			res.Start = start
			results <- res

			select {
			case <-stop:
				close(results)
				return
			default:
				continue
			}
		}
	}()

	return results
}

func (a *RepeatAgent) String() string {
	return fmt.Sprintf("RepeatAgent(%s %s)", a.BaseRequest.Method, a.BaseRequest.URL.String())
}

type IntervalAgent struct {
	BaseRequest *http.Request
	Body        []byte
	Interval    time.Duration
	Clock       interface {
		Now() time.Time
		Tick()
	}
}

func (a *IntervalAgent) Run(queue chan<- Task, stop <-chan struct{}) <-chan Result {
	results := make(chan Result)
	var wg sync.WaitGroup

	go func() {
		ticker := time.NewTicker(a.Interval)
		for {
			task := Task{copyRequest(a.BaseRequest, a.Body), make(chan Result, 1)}
			queue <- task

			go func(rc <-chan Result) {
				wg.Add(1)

				start := a.Clock.Now()
				res := <-rc
				res.Start = start
				results <- res

				wg.Done()
			}(task.Result)

			select {
			case <-stop:
				wg.Wait()
				close(results)
				ticker.Stop()
				return
			case <-ticker.C:
				continue
			}

		}
	}()

	return results
}

func (a *IntervalAgent) String() string {
	return fmt.Sprintf("IntervalAgent(%s %s every %s)", a.BaseRequest.Method, a.BaseRequest.URL.String(), a.Interval)
}

func copyRequest(orig *http.Request, body []byte) *http.Request {
	copy := *orig

	copy.Header = make(http.Header)
	for k, vv := range orig.Header {
		for _, v := range vv {
			copy.Header.Add(k, v)
		}
	}

	if body != nil {
		copy.Body = ioutil.NopCloser(bytes.NewReader(body))
	}

	return &copy
}
