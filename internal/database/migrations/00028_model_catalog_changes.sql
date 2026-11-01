-- +goose Up
CREATE TABLE model_catalog_changes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 model_id uuid NOT NULL REFERENCES model_catalog(id),
 provider text NOT NULL CHECK(provider IN ('liquid','openrouter','openai','anthropic')),
 model_name text NOT NULL,
 upstream_model_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('added','updated','retired','restored','price_changed','deleted')),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 source_url text NOT NULL,
 before_doc jsonb,
 after_doc jsonb NOT NULL
);
CREATE INDEX model_catalog_changes_recent ON model_catalog_changes(occurred_at DESC,id DESC);
CREATE INDEX model_catalog_changes_model ON model_catalog_changes(model_id,occurred_at DESC,id DESC);

-- Only public, normalized fields leave model_catalog. Raw upstream documents,
-- API credentials and research prompts never enter the journal.
-- +goose StatementBegin
CREATE FUNCTION catalog_change_snapshot(doc jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT jsonb_build_object(
  'display_name',doc->'display_name','enabled',doc->'enabled','available',doc->'available',
  'text_input',doc->'text_input','structured_output',doc->'structured_output',
  'context_length',doc->'context_length','max_input_tokens',doc->'max_input_tokens',
  'max_output_tokens',doc->'max_output_tokens',
  'price',CASE WHEN jsonb_typeof(doc->'current_price')='object' THEN jsonb_build_object(
   'currency',doc->'current_price'->'currency','rates',doc->'current_price'->'rates',
   'source',doc->'current_price'->'source','source_url',doc->'current_price'->'source_url') ELSE 'null'::jsonb END
 );
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION catalog_change_source(provider text,doc jsonb) RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN doc->'current_price'->>'source_url' LIKE 'https://%' THEN doc->'current_price'->>'source_url'
  WHEN provider='openai' THEN 'https://developers.openai.com/api/docs/models'
  WHEN provider='anthropic' THEN 'https://platform.claude.com/docs/en/models/overview'
  WHEN provider='liquid' THEN 'https://www.liquid.ai/models'
  ELSE 'https://openrouter.ai/models' END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION record_catalog_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous jsonb; current_doc jsonb; event_kind text;
BEGIN
 current_doc:=catalog_change_snapshot(NEW.doc);
 IF TG_OP='INSERT' THEN
  event_kind:='added';
 ELSE
  previous:=catalog_change_snapshot(OLD.doc);
  IF coalesce(NEW.doc->>'deleted_at','')<>'' AND coalesce(OLD.doc->>'deleted_at','')='' THEN event_kind:='deleted';
  ELSIF coalesce(NEW.doc->>'deleted_at','')<>'' THEN RETURN NEW;
  ELSIF previous=current_doc THEN RETURN NEW;
  ELSIF OLD.doc->>'available'='true' AND NEW.doc->>'available'='false' THEN event_kind:='retired';
  ELSIF OLD.doc->>'available'='false' AND NEW.doc->>'available'='true' THEN event_kind:='restored';
  ELSIF previous->'price' IS DISTINCT FROM current_doc->'price' THEN event_kind:='price_changed';
  ELSE event_kind:='updated'; END IF;
 END IF;
 INSERT INTO model_catalog_changes(model_id,provider,model_name,upstream_model_id,kind,source_url,before_doc,after_doc)
 VALUES(NEW.id,NEW.provider,coalesce(NEW.doc->>'display_name',NEW.model_id),NEW.model_id,event_kind,catalog_change_source(NEW.provider,NEW.doc),previous,current_doc);
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER record_catalog_change AFTER INSERT OR UPDATE OF doc ON model_catalog FOR EACH ROW EXECUTE FUNCTION record_catalog_change();

-- This journal begins now. Existing registrations and historical release dates
-- cannot be reconstructed reliably and are deliberately not fabricated.
-- +goose StatementBegin
CREATE FUNCTION preserve_catalog_changes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'catalog change history is immutable' USING ERRCODE='23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_catalog_changes BEFORE UPDATE OR DELETE ON model_catalog_changes FOR EACH ROW EXECUTE FUNCTION preserve_catalog_changes();

-- +goose Down
DROP TRIGGER record_catalog_change ON model_catalog;
DROP FUNCTION record_catalog_change();
DROP TRIGGER preserve_catalog_changes ON model_catalog_changes;
DROP FUNCTION preserve_catalog_changes();
DROP FUNCTION catalog_change_source(text,jsonb);
DROP FUNCTION catalog_change_snapshot(jsonb);
DROP TABLE model_catalog_changes;
