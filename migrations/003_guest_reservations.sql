BEGIN;
CREATE INDEX reservations_owner_start_idx ON reservations (user_id, starts_at DESC, id DESC);
COMMIT;
