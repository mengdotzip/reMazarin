package storage

import (
	"context"
	"testing"
	"time"
)

// Older builds persisted sessions.client_ip with BLOB storage class. SQLite never
// treats a BLOB as equal to a bound TEXT value, so IP session auth (WHERE
// client_ip = ?) silently failed to match those rows. The lookups CAST to TEXT
// (and migration 010 rewrites stored values) so such sessions authenticate again.
func TestIPSessionAuthMatchesBlobClientIP(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	u, _ := s.CreateUser(ctx, "meng", "pw")
	g, _ := s.CreateGroup(ctx, "g1", "")
	s.AddUserToGroup(ctx, u.ID, g.ID)

	const ip = "10.0.30.25"
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, client_ip, expires_at) VALUES (?,?,?,?)`,
		"tok", u.ID, []byte(ip), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	sg, err := s.ValidateSessionByIPAndGroups(ctx, ip)
	if err != nil {
		t.Fatalf("ValidateSessionByIPAndGroups should match BLOB-stored client_ip: %v", err)
	}
	if sg.Username != "meng" {
		t.Fatalf("got user %q, want meng", sg.Username)
	}

	if _, err := s.ValidateSessionByIP(ctx, ip); err != nil {
		t.Fatalf("ValidateSessionByIP should match BLOB-stored client_ip: %v", err)
	}
}

// A session orphaned by a deleted user (foreign keys were never enforced) with a
// later expiry must not shadow a valid co-located session on the same IP, and the
// lookup must only authorize a session whose user is in the allowed groups.
func TestIPSessionAuthSkipsOrphanAndHonorsGroups(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir() + "/orphan.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	old, _ := s.CreateUser(ctx, "oldadmin", "pw")
	meng, _ := s.CreateUser(ctx, "meng", "pw")
	g1, _ := s.CreateGroup(ctx, "g1", "")
	s.AddUserToGroup(ctx, meng.ID, g1.ID)

	const ip = "10.0.30.25"
	// old user's session expires later than meng's, so it sorts first.
	s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,client_ip,expires_at) VALUES (?,?,?,?)`,
		"tok_old", old.ID, ip, time.Now().Add(96*time.Hour))
	s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,client_ip,expires_at) VALUES (?,?,?,?)`,
		"tok_meng", meng.ID, ip, time.Now().Add(12*time.Hour))
	// Delete the user without cascade -> orphaned session lingers.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}

	sg, err := s.ValidateSessionByIPAndGroups(ctx, ip)
	if err != nil {
		t.Fatalf("orphan should not shadow valid session: %v", err)
	}
	if sg.Username != "meng" {
		t.Fatalf("got user %q, want meng", sg.Username)
	}

	if _, err := s.ValidateSessionByIPInGroups(ctx, ip, []int{g1.ID}); err != nil {
		t.Fatalf("meng is in g1 and should authorize: %v", err)
	}
	if _, err := s.ValidateSessionByIPInGroups(ctx, ip, []int{99}); err == nil {
		t.Fatalf("no session user is in group 99; expected no match")
	}
}

// IP/TCP renewal must extend every session a user holds on the IP, not just one,
// so TCP activity keeps a co-located browser session alive instead of letting it
// lapse. Sessions on other IPs or for other users must be left untouched.
func TestExtendUserSessionsByIPRenewsAllUserSessions(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir() + "/renew.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	meng, _ := s.CreateUser(ctx, "meng", "pw")
	other, _ := s.CreateUser(ctx, "other", "pw")

	const ip = "10.0.30.25"
	near := time.Now().Add(1 * time.Hour)
	// Two of meng's sessions on the same IP (e.g. browser + an earlier login),
	// one of meng's on a different IP, and one belonging to another user.
	s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,client_ip,expires_at) VALUES (?,?,?,?)`,
		"a", meng.ID, ip, near)
	s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,client_ip,expires_at) VALUES (?,?,?,?)`,
		"b", meng.ID, ip, near)
	s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,client_ip,expires_at) VALUES (?,?,?,?)`,
		"c", meng.ID, "10.0.30.99", near)
	s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,client_ip,expires_at) VALUES (?,?,?,?)`,
		"d", other.ID, ip, near)

	s.ExtendUserSessionsByIP(ctx, meng.ID, ip, 15*time.Hour)

	exp := func(tok string) time.Time {
		var e time.Time
		s.db.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash = ?`, tok).Scan(&e)
		return e
	}
	cutoff := time.Now().Add(10 * time.Hour)
	if !exp("a").After(cutoff) || !exp("b").After(cutoff) {
		t.Fatalf("both of meng's sessions on the IP should be extended: a=%s b=%s", exp("a"), exp("b"))
	}
	if exp("c").After(cutoff) {
		t.Fatalf("meng's session on a different IP must not be extended: %s", exp("c"))
	}
	if exp("d").After(cutoff) {
		t.Fatalf("another user's session on the IP must not be extended: %s", exp("d"))
	}
}

// With foreign_keys enforced, deleting a user must cascade to their sessions so
// no orphans are created in the first place.
func TestDeletingUserCascadesSessions(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir() + "/fk.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys not enabled: got %d err %v", fk, err)
	}

	u, _ := s.CreateUser(ctx, "tmp", "pw")
	if _, err := s.CreateSession(ctx, u.ID, time.Hour, "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, u.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("expected sessions to cascade-delete, found %d", n)
	}
}
