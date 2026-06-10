package proxy

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event outcomes recorded for every connection/request the proxy sees — not just
// the authorized happy path. The firehose (scans, junk, denied, TLS errors) is
// kept in memory only: per-outcome counters plus a capped recent-events ring.
// Writing a DB row per junk packet would turn a flood into a disk-write flood.
const (
	OutcomeServed      = "served"       // request/connection forwarded to a backend
	OutcomeDenied      = "denied"       // failed auth (bad/expired/forbidden)
	OutcomeRateLimited = "rate_limited" // throttled (429 / dropped)
	OutcomeBanned      = "banned"       // source IP is banned
	OutcomeNotFound    = "not_found"    // listener exists but no route for the Host
	OutcomeNoListener  = "no_listener"  // no route/listener for the port
	OutcomeTLSError    = "tls_error"    // TLS handshake failed (junk / plain HTTP to TLS)
	OutcomeTCPRejected = "tcp_rejected" // raw TCP/UDP connection not authorized
	OutcomeDialError   = "dial_error"   // backend dial failed
)

// recentEventsCap bounds the in-memory recent-events ring shown in the admin UI.
const recentEventsCap = 500

// Event is one recorded proxy event for the recent-events ring.
type Event struct {
	Time    time.Time `json:"time"`
	IP      string    `json:"ip"`
	Route   string    `json:"route"`
	Outcome string    `json:"outcome"`
}

var (
	routeCounters sync.Map // routeUrl → *int64 (served count, for the Route Activity panel)
	eventCounters sync.Map // outcome   → *int64 (per-outcome totals)

	eventsMu   sync.Mutex
	eventRing  = make([]Event, recentEventsCap)
	eventHead  int
	eventCount int
)

// RecordEvent records one proxy event: it bumps the per-outcome counter, pushes
// to the recent-events ring, and (for served outcomes) bumps the per-route
// served counter. All in-memory; counters reset on process restart.
func RecordEvent(ip, route, outcome string) {
	ev, _ := eventCounters.LoadOrStore(outcome, new(int64))
	atomic.AddInt64(ev.(*int64), 1)

	if outcome == OutcomeServed {
		rv, _ := routeCounters.LoadOrStore(route, new(int64))
		atomic.AddInt64(rv.(*int64), 1)
	}

	eventsMu.Lock()
	eventRing[eventHead] = Event{Time: time.Now(), IP: ip, Route: route, Outcome: outcome}
	eventHead = (eventHead + 1) % recentEventsCap
	if eventCount < recentEventsCap {
		eventCount++
	}
	eventsMu.Unlock()
}

// RecordRequest records a served request for routeUrl. Kept as a thin shim over
// RecordEvent for call sites that only count successful forwards.
func RecordRequest(routeUrl string) { RecordEvent("", routeUrl, OutcomeServed) }

// GetRouteStats returns a snapshot of per-route served counts.
func GetRouteStats() map[string]int64 {
	out := make(map[string]int64)
	routeCounters.Range(func(k, v any) bool {
		out[k.(string)] = atomic.LoadInt64(v.(*int64))
		return true
	})
	return out
}

// GetEventStats returns a snapshot of per-outcome event totals.
func GetEventStats() map[string]int64 {
	out := make(map[string]int64)
	eventCounters.Range(func(k, v any) bool {
		out[k.(string)] = atomic.LoadInt64(v.(*int64))
		return true
	})
	return out
}

// GetRecentEvents returns the recent-events ring, newest first.
func GetRecentEvents() []Event {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	out := make([]Event, 0, eventCount)
	// eventHead points at the next write slot; walk backwards for newest-first.
	for i := 0; i < eventCount; i++ {
		idx := (eventHead - 1 - i + recentEventsCap) % recentEventsCap
		out = append(out, eventRing[idx])
	}
	return out
}
