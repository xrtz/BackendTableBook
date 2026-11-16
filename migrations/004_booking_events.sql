BEGIN;

CREATE FUNCTION notify_booking_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_hall bigint;
DECLARE new_hall bigint;
BEGIN
    SELECT hall_id INTO new_hall FROM dining_tables WHERE id = NEW.table_id;
    IF TG_OP = 'UPDATE' AND OLD.table_id <> NEW.table_id THEN
        SELECT hall_id INTO old_hall FROM dining_tables WHERE id = OLD.table_id;
    END IF;
    PERFORM pg_notify('tablebook_booking', json_build_object(
        'owner', NEW.user_id, 'hall', new_hall, 'oldHall', old_hall
    )::text);
    RETURN NEW;
END;
$$;

CREATE TRIGGER booking_changed AFTER INSERT OR UPDATE ON reservations
FOR EACH ROW EXECUTE FUNCTION notify_booking_change();

COMMIT;
