package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/mdobak/go-xerrors"
)

// Built-in throttle tiers. Per-group overrides use the key "group:<id>".
const (
	TierAnonymous = "anonymous"
	TierSignedIn  = "signed-in"
)

// ThrottlePolicy is a per-tier rate-limit + auto-ban rule. A zero RatePerSec or
// disabled flag means "no limit"; a disabled BanEnabled means "never auto-ban".
type ThrottlePolicy struct {
	Tier           string  `json:"tier"`
	Enabled        bool    `json:"enabled"`
	RatePerSec     float64 `json:"rate_per_sec"`
	Burst          int     `json:"burst"`
	BanEnabled     bool    `json:"ban_enabled"`
	BanThreshold   int     `json:"ban_threshold"`
	BanWindowSec   int     `json:"ban_window_sec"`
	BanDurationSec int     `json:"ban_duration_sec"`
}

// BannedIP is one active or scheduled IP ban. ExpiresAt is nil for a ban that
// holds until an admin clears it.
type BannedIP struct {
	IP        string     `json:"ip"`
	Reason    string     `json:"reason"`
	Tier      string     `json:"tier"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Storage) GetThrottlePolicies(ctx context.Context) ([]ThrottlePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tier, enabled, rate_per_sec, burst,
		       ban_enabled, ban_threshold, ban_window_sec, ban_duration_sec
		FROM throttle_policies ORDER BY tier`)
	if err != nil {
		return nil, xerrors.Newf("query throttle policies: %w", err)
	}
	defer rows.Close()
	var out []ThrottlePolicy
	for rows.Next() {
		var p ThrottlePolicy
		if err := rows.Scan(&p.Tier, &p.Enabled, &p.RatePerSec, &p.Burst,
			&p.BanEnabled, &p.BanThreshold, &p.BanWindowSec, &p.BanDurationSec); err != nil {
			return nil, xerrors.Newf("scan throttle policy: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertThrottlePolicy creates or replaces the policy for a tier.
func (s *Storage) UpsertThrottlePolicy(ctx context.Context, p ThrottlePolicy) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO throttle_policies
			(tier, enabled, rate_per_sec, burst, ban_enabled, ban_threshold, ban_window_sec, ban_duration_sec)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tier) DO UPDATE SET
			enabled          = excluded.enabled,
			rate_per_sec     = excluded.rate_per_sec,
			burst            = excluded.burst,
			ban_enabled      = excluded.ban_enabled,
			ban_threshold    = excluded.ban_threshold,
			ban_window_sec   = excluded.ban_window_sec,
			ban_duration_sec = excluded.ban_duration_sec`,
		p.Tier, p.Enabled, p.RatePerSec, p.Burst,
		p.BanEnabled, p.BanThreshold, p.BanWindowSec, p.BanDurationSec)
	if err != nil {
		return xerrors.Newf("upsert throttle policy: %w", err)
	}
	return nil
}

// DeleteThrottlePolicy removes a per-group override. The two built-in tiers
// cannot be deleted (only disabled).
func (s *Storage) DeleteThrottlePolicy(ctx context.Context, tier string) error {
	if tier == TierAnonymous || tier == TierSignedIn {
		return xerrors.Newf("cannot delete built-in tier %q", tier)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM throttle_policies WHERE tier = ?`, tier)
	return err
}

// GetActiveBans returns bans that are still in force (no expiry, or expiry in
// the future).
func (s *Storage) GetActiveBans(ctx context.Context) ([]BannedIP, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ip, reason, tier, created_at, expires_at
		FROM banned_ips
		WHERE expires_at IS NULL OR expires_at > datetime('now')
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, xerrors.Newf("query bans: %w", err)
	}
	defer rows.Close()
	var out []BannedIP
	for rows.Next() {
		var b BannedIP
		var exp sql.NullTime
		if err := rows.Scan(&b.IP, &b.Reason, &b.Tier, &b.CreatedAt, &exp); err != nil {
			return nil, xerrors.Newf("scan ban: %w", err)
		}
		if exp.Valid {
			b.ExpiresAt = &exp.Time
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// InsertBan records (or refreshes) a ban. expiresAt nil means until cleared.
func (s *Storage) InsertBan(ctx context.Context, ip, reason, tier string, expiresAt *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO banned_ips (ip, reason, tier, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET
			reason     = excluded.reason,
			tier       = excluded.tier,
			created_at = CURRENT_TIMESTAMP,
			expires_at = excluded.expires_at`,
		ip, reason, tier, expiresAt)
	if err != nil {
		return xerrors.Newf("insert ban: %w", err)
	}
	return nil
}

// DeleteBan lifts a ban (manual unban).
func (s *Storage) DeleteBan(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM banned_ips WHERE ip = ?`, ip)
	return err
}

// CleanupExpiredBans removes bans whose expiry has passed.
func (s *Storage) CleanupExpiredBans(ctx context.Context) {
	s.db.ExecContext(ctx,
		`DELETE FROM banned_ips WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')`)
}
