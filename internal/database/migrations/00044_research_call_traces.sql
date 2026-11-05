-- +goose Up
CREATE TABLE research_call_traces (
 call_id uuid PRIMARY KEY REFERENCES ai_calls(id) ON DELETE CASCADE,
 key_id text NOT NULL CHECK(length(key_id) BETWEEN 1 AND 256),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 1 AND 3146752),
 captured_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>captured_at AND expires_at<=captured_at+interval '168 hours')
);
CREATE INDEX research_call_traces_expiration ON research_call_traces(expires_at);

-- +goose Down
LOCK TABLE research_call_traces IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM research_call_traces) THEN
  RAISE EXCEPTION 'encrypted research traces prevent downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE research_call_traces;
