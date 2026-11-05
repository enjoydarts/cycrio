-- +goose Up
ALTER TABLE research_call_costs
 ADD COLUMN request_tool_limit integer CHECK(request_tool_limit BETWEEN 1 AND 8),
 ADD COLUMN tool_actions jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(tool_actions)='array' AND jsonb_array_length(tool_actions)<=32),
 ADD COLUMN response_state text NOT NULL DEFAULT '' CHECK(response_state IN ('','completed','incomplete','failed','cancelled','queued','in_progress','unknown')),
 ADD COLUMN failure_code text NOT NULL DEFAULT '' CHECK(failure_code IN ('','invalid_results','incomplete_response','search_unverified','provider_unavailable','invalid_response','invalid_request','search_limit_exceeded','http_error','request_failed','unknown')),
 ADD COLUMN http_status integer CHECK(http_status BETWEEN 100 AND 599);

-- Shared calls were introduced in v41 with max_tool_calls=1. This records
-- only the historical request bound; classification and cost remain unchanged.
UPDATE research_call_costs d SET request_tool_limit=1
FROM ai_calls c WHERE c.id=d.call_id AND c.provider='openai'
 AND (SELECT count(*) FROM price_research_items i WHERE i.call_id=c.id)>=2;

-- +goose Down
LOCK TABLE research_call_costs IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM research_call_costs WHERE request_tool_limit IS NOT NULL OR tool_actions<>'[]'::jsonb OR response_state<>'' OR failure_code<>'' OR http_status IS NOT NULL) THEN
  RAISE EXCEPTION 'research response evidence prevents downgrade' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE research_call_costs DROP COLUMN request_tool_limit, DROP COLUMN tool_actions, DROP COLUMN response_state, DROP COLUMN failure_code, DROP COLUMN http_status;
