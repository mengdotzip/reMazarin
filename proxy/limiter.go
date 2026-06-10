package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"reMazarin/storage"
	"sync"
	"sync/atomic"
	"time"
)

// The limiter enforces per-tier rate limits and auto-bans, all keyed by client
// IP. State lives in memory on the hot path; bans are mirrored to the DB so they
// survive a restart. Policies are reloaded from the DB through the same refresh
// that drives the route/auth cache.
//
// Tiers: an IP is classified "anonymous" until a request authenticates, at which
// point SetTier promotes it to "signed-in" or a per-group override. New/unseen
// IPs default to the strictest (anonymous) tier — which is exactly the auth-page
// case the operator wants protected.

const (
	// ipBucketIdle is how long an IP bucket may sit untouched before the sweep
	// evicts it to bound memory.
	ipBucketIdle = 30 * time.Minute
)

type ipBucket struct {
	mu          sync.Mutex
	tier        string
	tokens      float64
	lastRefill  time.Time
	failCount   int
	windowStart time.Time
	lastSeen    time.Time
}

var (
	ipBuckets sync.Map     // ip → *ipBucket
	policies  atomic.Value // map[string]storage.ThrottlePolicy
	banned    = &banSet{m: make(map[string]time.Time)}
)

// banSet is the in-memory ban index. A zero expiry means "until manually cleared".
type banSet struct {
	mu sync.RWMutex
	m  map[string]time.Time
}

func (b *banSet) has(ip string) bool {
	b.mu.RLock()
	exp, ok := b.m[ip]
	b.mu.RUnlock()
	if !ok {
		return false
	}
	if exp.IsZero() {
		return true // permanent
	}
	if time.Now().After(exp) {
		b.mu.Lock()
		if e, ok := b.m[ip]; ok && !e.IsZero() && time.Now().After(e) {
			delete(b.m, ip)
		}
		b.mu.Unlock()
		return false
	}
	return true
}

func (b *banSet) set(ip string, exp time.Time) {
	b.mu.Lock()
	b.m[ip] = exp
	b.mu.Unlock()
}

func (b *banSet) del(ip string) {
	b.mu.Lock()
	delete(b.m, ip)
	b.mu.Unlock()
}

func (b *banSet) replace(m map[string]time.Time) {
	b.mu.Lock()
	b.m = m
	b.mu.Unlock()
}

func init() { policies.Store(map[string]storage.ThrottlePolicy{}) }

func policyFor(tier string) (storage.ThrottlePolicy, bool) {
	m := policies.Load().(map[string]storage.ThrottlePolicy)
	p, ok := m[tier]
	return p, ok
}

// ResolveTier picks the most specific enabled tier for an authenticated user:
// a per-group override if one of the user's groups has an enabled policy,
// otherwise the built-in signed-in tier.
func ResolveTier(groupIDs []int) string {
	m := policies.Load().(map[string]storage.ThrottlePolicy)
	for _, id := range groupIDs {
		key := fmt.Sprintf("group:%d", id)
		if p, ok := m[key]; ok && p.Enabled {
			return key
		}
	}
	return storage.TierSignedIn
}

func bucketFor(ip string) *ipBucket {
	if v, ok := ipBuckets.Load(ip); ok {
		return v.(*ipBucket)
	}
	// lastRefill is left zero so the first Allow fills the bucket to its burst.
	b := &ipBucket{tier: storage.TierAnonymous, lastSeen: time.Now()}
	actual, _ := ipBuckets.LoadOrStore(ip, b)
	return actual.(*ipBucket)
}

// SetTier promotes an IP to the tier resolved from a successful authentication.
// It never demotes to anonymous — the strictest classification an IP has earned
// in its lifetime sticks until the bucket is evicted.
func SetTier(ip, tier string) {
	b := bucketFor(ip)
	b.mu.Lock()
	if b.tier != tier {
		b.tier = tier
	}
	b.lastSeen = time.Now()
	b.mu.Unlock()
}

// IsBanned reports whether the IP is currently banned.
func IsBanned(ip string) bool { return banned.has(ip) }

// Allow applies the IP's tier rate limit, consuming one token. It returns false
// with a Retry-After (seconds) when the bucket is empty. A disabled tier, a
// zero rate, or no policy means unlimited.
func Allow(ip string) (bool, int) {
	b := bucketFor(ip)
	b.mu.Lock()
	defer b.mu.Unlock()

	pol, ok := policyFor(b.tier)
	if !ok || !pol.Enabled || pol.RatePerSec <= 0 {
		b.lastSeen = time.Now()
		return true, 0
	}

	burst := float64(pol.Burst)
	if burst < 1 {
		burst = 1
	}
	now := time.Now()
	if b.lastRefill.IsZero() {
		b.tokens = burst
	} else {
		b.tokens += now.Sub(b.lastRefill).Seconds() * pol.RatePerSec
	}
	if b.tokens > burst {
		b.tokens = burst
	}
	b.lastRefill = now
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}
	retry := int((1-b.tokens)/pol.RatePerSec) + 1
	return false, retry
}

// RecordFailure counts one failure (denied auth, rate-limit hit, TLS junk, etc.)
// for the IP against its tier's auto-ban policy, banning it once the threshold is
// crossed within the sliding window.
func RecordFailure(ip string) {
	b := bucketFor(ip)
	b.mu.Lock()

	pol, ok := policyFor(b.tier)
	if !ok || !pol.BanEnabled || pol.BanThreshold <= 0 {
		b.lastSeen = time.Now()
		b.mu.Unlock()
		return
	}

	now := time.Now()
	window := time.Duration(pol.BanWindowSec) * time.Second
	if b.windowStart.IsZero() || (window > 0 && now.Sub(b.windowStart) > window) {
		b.windowStart = now
		b.failCount = 0
	}
	b.failCount++
	b.lastSeen = now
	tripped := b.failCount >= pol.BanThreshold
	tier := b.tier
	if tripped {
		b.failCount = 0
		b.windowStart = time.Time{}
	}
	b.mu.Unlock()

	if tripped {
		reason := fmt.Sprintf("auto: %d failures within %ds (%s)", pol.BanThreshold, pol.BanWindowSec, tier)
		Ban(ip, reason, tier, pol.BanDurationSec)
	}
}

// Ban bans an IP for durationSec (0 = until manually cleared), updating both the
// in-memory set and the DB.
func Ban(ip, reason, tier string, durationSec int) {
	var expiry time.Time
	var exp *time.Time
	if durationSec > 0 {
		expiry = time.Now().Add(time.Duration(durationSec) * time.Second)
		exp = &expiry
	}
	banned.set(ip, expiry)
	if authStore != nil {
		if err := authStore.InsertBan(context.Background(), ip, reason, tier, exp); err != nil {
			slog.Error("persist ban failed", "ip", ip, "error", err)
		}
	}
	slog.Warn("ip banned", "ip", ip, "reason", reason, "duration_sec", durationSec)
}

// Unban lifts a ban from both the in-memory set and the DB.
func Unban(ip string) {
	banned.del(ip)
	if authStore != nil {
		authStore.DeleteBan(context.Background(), ip)
	}
	slog.Info("ip unbanned", "ip", ip)
}

// BanIP / UnbanIP are the admin-facing manual controls.
func BanIP(ip string, durationSec int) { Ban(ip, "manual", "manual", durationSec) }
func UnbanIP(ip string)                { Unban(ip) }

// GetActiveBans returns the persisted active bans for the admin UI.
func GetActiveBans() []storage.BannedIP {
	if authStore == nil {
		return nil
	}
	bans, err := authStore.GetActiveBans(context.Background())
	if err != nil {
		slog.Error("get active bans failed", "error", err)
		return nil
	}
	return bans
}

// reloadThrottle refreshes the policy snapshot and ban set from the DB. Called
// from refreshCache so throttle config picks up admin changes immediately and on
// the periodic tick.
func reloadThrottle() {
	if authStore == nil {
		return
	}
	ctx := context.Background()

	if ps, err := authStore.GetThrottlePolicies(ctx); err == nil {
		m := make(map[string]storage.ThrottlePolicy, len(ps))
		for _, p := range ps {
			m[p.Tier] = p
		}
		policies.Store(m)
	} else {
		slog.Error("reload throttle policies failed", "error", err)
	}

	if bans, err := authStore.GetActiveBans(ctx); err == nil {
		m := make(map[string]time.Time, len(bans))
		for _, b := range bans {
			if b.ExpiresAt != nil {
				m[b.IP] = *b.ExpiresAt
			} else {
				m[b.IP] = time.Time{}
			}
		}
		banned.replace(m)
	} else {
		slog.Error("reload bans failed", "error", err)
	}
}

// sweepBuckets evicts idle IP buckets to bound memory. Called from the periodic
// auth ticker.
func sweepBuckets() {
	cutoff := time.Now().Add(-ipBucketIdle)
	ipBuckets.Range(func(k, v any) bool {
		b := v.(*ipBucket)
		b.mu.Lock()
		idle := b.lastSeen.Before(cutoff)
		b.mu.Unlock()
		if idle {
			ipBuckets.Delete(k)
		}
		return true
	})
}
