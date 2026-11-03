-- +goose Up
ALTER TABLE subscriptions ADD COLUMN display_name text
 CHECK(display_name IS NULL OR (char_length(display_name) BETWEEN 1 AND 200 AND display_name !~ '[[:cntrl:]]'));

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM subscriptions WHERE display_name IS NOT NULL) THEN
  RAISE EXCEPTION 'Cannot remove subscription display names while saved names exist';
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE subscriptions DROP COLUMN display_name;
