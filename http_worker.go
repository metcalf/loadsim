package loadsim

import (
	"net/http"
	"sync"
	"time"
)

type Worker interface {
	Run(<-chan Task)
}

type HTTPWorker struct {
	Client http.Client
	Clock  interface {
		Now() time.Time
	}
}

func (w *HTTPWorker) Run(queue <-chan Task) {
	var wg sync.WaitGroup
	for task := range queue {
		go func(t Task) {
			wg.Add(1)

			resp, err := w.Client.Do(t.Request)
			res := Result{End: w.Clock.Now()}
			if err != nil {
				res.Err = err
			} else {
				res.StatusCode = resp.StatusCode
			}
			t.Result <- res

			wg.Done()
		}(task)
	}
	wg.Wait()
}
