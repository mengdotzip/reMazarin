package proxy

import (
	"sync"
	"sync/atomic"
)

var routeCounters sync.Map // string → *int64

// RecordRequest increments the in-memory request counter for routeUrl.
// Counters reset on process restart.
func RecordRequest(routeUrl string) {
	v, _ := routeCounters.LoadOrStore(routeUrl, new(int64))
	atomic.AddInt64(v.(*int64), 1)
}

// GetRouteStats returns a snapshot of all per-route request counts.
func GetRouteStats() map[string]int64 {
	out := make(map[string]int64)
	routeCounters.Range(func(k, v any) bool {
		out[k.(string)] = atomic.LoadInt64(v.(*int64))
		return true
	})
	return out
}
