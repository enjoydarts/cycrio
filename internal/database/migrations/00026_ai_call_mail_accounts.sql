-- +goose Up
ALTER TABLE ai_calls ADD COLUMN mail_account_id uuid;
ALTER TABLE ai_calls ADD CONSTRAINT ai_calls_mail_account_owner_fk
 FOREIGN KEY(consuming_user_id,mail_account_id) REFERENCES mail_accounts(owner_id,id);
ALTER TABLE ai_calls ADD CONSTRAINT ai_calls_mail_account_user_scope
 CHECK(mail_account_id IS NULL OR scope='user');

-- Only processing items belonging to the consuming user establish attribution.
-- Deleted analysis records deliberately remain unattributed.
UPDATE ai_calls c SET mail_account_id=i.account_id
 FROM mail_processing_items i
 WHERE c.scope='user' AND c.operation_id=i.id AND c.consuming_user_id=i.owner_id;

-- +goose StatementBegin
CREATE FUNCTION preserve_ai_call_mail_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.mail_account_id IS DISTINCT FROM OLD.mail_account_id
   OR NEW.consuming_user_id IS DISTINCT FROM OLD.consuming_user_id
   OR NEW.scope IS DISTINCT FROM OLD.scope
   OR NEW.operation_id IS DISTINCT FROM OLD.operation_id THEN
   RAISE EXCEPTION 'AI call attribution is immutable' USING ERRCODE='23514';
  END IF;
 ELSIF NEW.scope='user' AND NEW.mail_account_id IS NULL THEN
  SELECT account_id INTO NEW.mail_account_id FROM mail_processing_items
   WHERE id=NEW.operation_id AND owner_id=NEW.consuming_user_id;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER ai_call_mail_account_attribution BEFORE INSERT OR UPDATE ON ai_calls
 FOR EACH ROW EXECUTE FUNCTION preserve_ai_call_mail_account();
CREATE INDEX ai_calls_mail_account_month ON ai_calls(consuming_user_id,mail_account_id,started_at);

-- +goose Down
-- Retained attribution can only be removed when Up can reconstruct it exactly.
-- Follow the worker's items -> calls lock order. Keep the reconstruction source
-- stable and serialize INSERT until the column has been removed.
LOCK TABLE mail_processing_items IN SHARE MODE;
LOCK TABLE ai_calls IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM ai_calls c WHERE c.mail_account_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM mail_processing_items i
    WHERE i.id=c.operation_id AND i.owner_id=c.consuming_user_id
     AND i.account_id=c.mail_account_id)
 ) THEN
  RAISE EXCEPTION 'Cannot remove retained AI call mail attribution';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER ai_call_mail_account_attribution ON ai_calls;
DROP FUNCTION preserve_ai_call_mail_account();
ALTER TABLE ai_calls DROP COLUMN mail_account_id;
