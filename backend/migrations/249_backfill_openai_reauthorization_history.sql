-- Persist the OpenAI 401 reauthorization history that can be proven from the
-- existing management audit log. Existing exact XIASS state always wins.
WITH audit_candidates AS (
    SELECT
        l.id,
        l.created_at,
        CASE
            WHEN l.path = '/api/v1/admin/accounts/:id/apply-oauth-credentials' THEN 'success'
            ELSE 'attempt'
        END AS event_kind,
        COALESCE(
            CASE WHEN COALESCE(l.extra->>'account_id', '') ~ '^[0-9]+$' THEN (l.extra->>'account_id')::BIGINT END,
            CASE WHEN COALESCE(l.extra->'params'->>'account_id', '') ~ '^[0-9]+$' THEN (l.extra->'params'->>'account_id')::BIGINT END,
            CASE WHEN COALESCE(l.extra->'params'->>'id', '') ~ '^[0-9]+$' THEN (l.extra->'params'->>'id')::BIGINT END,
            CASE
                WHEN l.request_body ~ '"account_id"[[:space:]]*:[[:space:]]*[0-9]+'
                THEN (substring(l.request_body FROM '"account_id"[[:space:]]*:[[:space:]]*([0-9]+)'))::BIGINT
            END
        ) AS account_id
    FROM audit_logs l
    WHERE l.method = 'POST'
      AND l.status_code < 400
      AND l.path IN (
          '/api/v1/admin/openai/reauthorization/tasks',
          '/api/v1/admin/openai/accounts/:id/reauthorize',
          '/api/v1/admin/openai/team-child/accounts/:account_id/reauthorize',
          '/api/v1/admin/accounts/:id/apply-oauth-credentials'
      )
), grouped_evidence AS (
    SELECT
        a.id AS account_id,
        a.last_used_at,
        COALESCE(array_agg(DISTINCT c.created_at ORDER BY c.created_at) FILTER (WHERE c.event_kind = 'attempt'), ARRAY[]::TIMESTAMPTZ[]) AS attempt_times,
        COALESCE(array_agg(DISTINCT c.created_at ORDER BY c.created_at) FILTER (WHERE c.event_kind = 'success'), ARRAY[]::TIMESTAMPTZ[]) AS explicit_success_times,
        COUNT(DISTINCT c.id)::INT AS evidence_count
    FROM accounts a
    JOIN audit_candidates c ON c.account_id = a.id
    WHERE a.deleted_at IS NULL
      AND a.platform = 'openai'
      AND a.type = 'oauth'
      AND a.parent_account_id IS NULL
    GROUP BY a.id, a.last_used_at
), derived_evidence AS (
    SELECT
        grouped.*,
        CASE
            WHEN cardinality(explicit_success_times) > 0 THEN explicit_success_times
            WHEN inferred.inferred_success_at IS NOT NULL THEN ARRAY[inferred.inferred_success_at]::TIMESTAMPTZ[]
            ELSE ARRAY[]::TIMESTAMPTZ[]
        END AS success_times
    FROM grouped_evidence grouped
    LEFT JOIN LATERAL (
        SELECT MAX(attempt_at) AS inferred_success_at
        FROM unnest(grouped.attempt_times) AS attempt_at
        WHERE grouped.last_used_at IS NOT NULL
          AND grouped.last_used_at > attempt_at
    ) inferred ON TRUE
), backfill_states AS (
    SELECT
        account_id,
        jsonb_strip_nulls(jsonb_build_object(
            'version', 1,
            'tracking_started_at', COALESCE(attempt_times[1], success_times[1]),
            'attempt_count', GREATEST(cardinality(attempt_times), cardinality(success_times)),
            'success_count', cardinality(success_times),
            'first_attempt_at', CASE
                WHEN attempt_times[1] IS NULL THEN success_times[1]
                WHEN success_times[1] IS NULL THEN attempt_times[1]
                ELSE LEAST(attempt_times[1], success_times[1])
            END,
            'last_attempt_at', CASE
                WHEN attempt_times[cardinality(attempt_times)] IS NULL THEN success_times[cardinality(success_times)]
                WHEN success_times[cardinality(success_times)] IS NULL THEN attempt_times[cardinality(attempt_times)]
                ELSE GREATEST(attempt_times[cardinality(attempt_times)], success_times[cardinality(success_times)])
            END,
            'first_succeeded_at', success_times[1],
            'last_succeeded_at', success_times[cardinality(success_times)],
            'successful_authorization_times', to_jsonb(success_times),
            'last_result', CASE WHEN cardinality(success_times) > 0 THEN 'success' ELSE '' END,
            'last_result_at', COALESCE(success_times[cardinality(success_times)], attempt_times[cardinality(attempt_times)]),
            'last_event_key', 'migration-249:' || account_id::TEXT,
            'history_source', 'startup_audit_backfill',
            'history_confidence', 'inferred',
            'legacy_evidence_count', evidence_count
        )) AS state
    FROM derived_evidence
)
UPDATE accounts a
SET extra = jsonb_set(COALESCE(a.extra, '{}'::jsonb), '{xiass_openai_reauthorization_state}', states.state, TRUE)
FROM backfill_states states
WHERE a.id = states.account_id
  AND (
      NOT (COALESCE(a.extra, '{}'::jsonb) ? 'xiass_openai_reauthorization_state')
      OR COALESCE(a.extra->'xiass_openai_reauthorization_state'->>'last_result', '') = 'legacy_unknown'
      OR COALESCE(a.extra->'xiass_openai_reauthorization_state'->>'history_confidence', '') = 'unknown'
  );

-- Accounts with no retained proof begin an exact XIASS baseline at upgrade
-- time. Their next detected 401 is therefore authorization number one.
UPDATE accounts a
SET extra = jsonb_set(
    COALESCE(a.extra, '{}'::jsonb),
    '{xiass_openai_reauthorization_state}',
    jsonb_build_object(
        'version', 1,
        'tracking_started_at', NOW(),
        'attempt_count', 0,
        'success_count', 0,
        'history_source', 'startup_baseline',
        'history_confidence', 'exact'
    ),
    TRUE
)
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.type = 'oauth'
  AND a.parent_account_id IS NULL
  AND (
      NOT (COALESCE(a.extra, '{}'::jsonb) ? 'xiass_openai_reauthorization_state')
      OR COALESCE(a.extra->'xiass_openai_reauthorization_state'->>'last_result', '') = 'legacy_unknown'
      OR COALESCE(a.extra->'xiass_openai_reauthorization_state'->>'history_confidence', '') = 'unknown'
  );
