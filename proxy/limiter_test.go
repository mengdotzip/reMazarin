package proxy

import (
	"reMazarin/storage"
	"testing"
	"time"
)

// setPolicy installs a single-tier policy snapshot for a test.
func setPolicy(p storage.ThrottlePolicy) {
	policies.Store(map[string]storage.ThrottlePolicy{p.Tier: p})
}

func TestAllowTokenBucket(t *testing.T) {
	setPolicy(storage.ThrottlePolicy{Tier: storage.TierAnonymous, Enabled: true, RatePerSec: 1, Burst: 2})
	ip := "10.1.0.1"
	ipBuckets.Delete(ip)

	// Burst of 2 → two immediate allows, third denied with a positive retry.
	if ok, _ := Allow(ip); !ok {
		t.Fatal("first request should be allowed")
	}
	if ok, _ := Allow(ip); !ok {
		t.Fatal("second request (within burst) should be allowed")
	}
	if ok, retry := Allow(ip); ok || retry < 1 {
		t.Fatalf("third request should be denied with retry, got ok=%v retry=%d", ok, retry)
	}
}

func TestAllowUnlimitedWhenDisabled(t *testing.T) {
	setPolicy(storage.ThrottlePolicy{Tier: storage.TierAnonymous, Enabled: false})
	ip := "10.1.0.2"
	ipBuckets.Delete(ip)
	for i := 0; i < 100; i++ {
		if ok, _ := Allow(ip); !ok {
			t.Fatal("a disabled policy must never rate-limit")
		}
	}
}

func TestRecordFailureBansAtThreshold(t *testing.T) {
	authStore = nil // exercise the memory-only path (no DB write)
	setPolicy(storage.ThrottlePolicy{
		Tier: storage.TierAnonymous, BanEnabled: true,
		BanThreshold: 3, BanWindowSec: 60, BanDurationSec: 60,
	})
	ip := "10.1.0.3"
	ipBuckets.Delete(ip)
	banned.del(ip)

	RecordFailure(ip)
	RecordFailure(ip)
	if IsBanned(ip) {
		t.Fatal("must not ban before the threshold is reached")
	}
	RecordFailure(ip)
	if !IsBanned(ip) {
		t.Fatal("must ban once the threshold is reached")
	}
	Unban(ip)
	if IsBanned(ip) {
		t.Fatal("unban must clear the ban")
	}
}

func TestBanExpiry(t *testing.T) {
	ip := "10.1.0.4"

	banned.set(ip, time.Now().Add(-time.Second)) // already expired
	if IsBanned(ip) {
		t.Fatal("an expired ban must not hold")
	}
	banned.set(ip, time.Time{}) // zero expiry = permanent
	if !IsBanned(ip) {
		t.Fatal("a permanent ban must hold")
	}
	banned.del(ip)
}

func TestFailureWindowResets(t *testing.T) {
	authStore = nil
	setPolicy(storage.ThrottlePolicy{
		Tier: storage.TierAnonymous, BanEnabled: true,
		BanThreshold: 3, BanWindowSec: 1, BanDurationSec: 60,
	})
	ip := "10.1.0.5"
	ipBuckets.Delete(ip)
	banned.del(ip)

	RecordFailure(ip)
	RecordFailure(ip)
	time.Sleep(1100 * time.Millisecond) // window elapses → counter resets
	RecordFailure(ip)
	RecordFailure(ip)
	if IsBanned(ip) {
		t.Fatal("failures spread across separate windows must not accumulate into a ban")
	}
}
