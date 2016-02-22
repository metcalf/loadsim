package loadsim

import (
	"sync"
	"time"

	"gopkg.in/throttled/throttled.v2"
	"gopkg.in/throttled/throttled.v2/store/memstore"
)

type Limiter interface {
	Allow(Task) bool
	Report(Task, Result)
}

const decayFactor = 0.2
const defaultEstimate = 100 * time.Millisecond
const maxEstimate = 60 * time.Second

type wallClockLimiter struct {
	limiter   throttled.RateLimiter
	mutex     sync.Mutex
	estimates map[string]time.Duration
}

type clockedMemStore struct {
	throttled.Store

	clock interface {
		Now() time.Time
	}
}

func (s *clockedMemStore) GetWithTime(key string) (int64, time.Time, error) {
	val, _, err := s.Store.GetWithTime(key)
	now := s.clock.Now()
	if err != nil {
		return val, now, err
	}

	return val, now, nil
}

func NewWallClockLimiter(burst, rate time.Duration, clock Clock) (Limiter, error) {
	var l wallClockLimiter

	l.estimates = make(map[string]time.Duration)

	store, err := memstore.New(65536)
	if err != nil {
		return nil, err
	}

	quota := throttled.RateQuota{
		throttled.PerSec(int(rate / time.Millisecond)),
		int(burst / time.Millisecond),
	}
	l.limiter, err = throttled.NewGCRARateLimiter(&clockedMemStore{store, clock}, quota)
	if err != nil {
		return nil, err
	}

	return &l, nil
}

func (l *wallClockLimiter) Allow(task Task) bool {
	key := l.key(task)

	l.mutex.Lock()
	est, ok := l.estimates[key]
	l.mutex.Unlock()

	if !ok {
		est = defaultEstimate
	}

	limited, _, err := l.limiter.RateLimit(key, int(est/time.Millisecond))
	if err != nil {
		panic(err)
	}

	return !limited
}

func (l *wallClockLimiter) Report(task Task, res Result) {
	key := l.key(task)
	dur := res.End.Sub(res.WorkStart)

	l.mutex.Lock()
	defer l.mutex.Unlock()
	est, ok := l.estimates[key]
	var newEst time.Duration
	if !ok {
		newEst = defaultEstimate
	} else {
		// Expansion of the following to reduce floating point errors (I think...)
		// newEst = time.Duration((1.0-decayFactor)*float64(est) + decayFactor*float64(dur))
		newEst = est - time.Duration(decayFactor*float64(est)-decayFactor*float64(dur))

		if newEst > maxEstimate {
			newEst = maxEstimate
		}
	}

	l.estimates[key] = newEst
}

func (l *wallClockLimiter) key(task Task) string {
	user, _, _ := task.Request.BasicAuth()
	return user
}
