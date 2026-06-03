-- Foreign keys were not enforced (PRAGMA foreign_keys defaults off), so deleting
-- a user left their sessions behind as orphans. An orphaned session with a late
-- expiry could shadow a valid co-located session and break IP session auth.
-- Remove sessions whose user no longer exists.
DELETE FROM sessions WHERE user_id NOT IN (SELECT id FROM users);
