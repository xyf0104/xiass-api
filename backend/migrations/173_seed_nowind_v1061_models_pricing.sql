-- v1.0.61 NoWind model/pricing compatibility seed.
--
-- Keep this migration additive and idempotent: existing customized channels and
-- groups must keep their current values. The goal is to make fresh installs and
-- upgraded empty/default installs show the new GPT-5.6 and Claude Fable/Sonnet
-- prices in both user and admin pricing screens.

DO $$
DECLARE
    v_codex_channel_id BIGINT;
    v_claude_channel_id BIGINT;
BEGIN
    SELECT id INTO v_codex_channel_id
    FROM channels
    WHERE name = 'Codex 号池'
    ORDER BY id
    LIMIT 1;

    SELECT id INTO v_claude_channel_id
    FROM channels
    WHERE name = 'jojo-max-claude'
    ORDER BY id
    LIMIT 1;

    IF v_codex_channel_id IS NOT NULL THEN
        INSERT INTO channel_model_pricing (
            channel_id, platform, billing_mode, models, input_price, output_price,
            cache_write_price, cache_read_price, image_output_price, per_request_price
        )
        SELECT v_codex_channel_id, 'openai', 'token', candidate.models,
               candidate.input_price, candidate.output_price, candidate.cache_write_price,
               candidate.cache_read_price, 0.00000000, NULL
        FROM (
            VALUES
                ('["gpt-5.6-sol"]'::jsonb,   0.000005000000::numeric, 0.000030000000::numeric, 0.000004000000::numeric, 0.000000500000::numeric),
                ('["gpt-5.6-terra"]'::jsonb, 0.000005000000::numeric, 0.000030000000::numeric, 0.000004000000::numeric, 0.000000500000::numeric),
                ('["gpt-5.6-luna"]'::jsonb,  0.000005000000::numeric, 0.000030000000::numeric, 0.000004000000::numeric, 0.000000500000::numeric)
        ) AS candidate(models, input_price, output_price, cache_write_price, cache_read_price)
        WHERE NOT EXISTS (
            SELECT 1
            FROM channel_model_pricing cmp
            CROSS JOIN LATERAL jsonb_array_elements_text(cmp.models) AS model_name(value)
            WHERE cmp.channel_id = v_codex_channel_id
              AND cmp.platform = 'openai'
              AND cmp.billing_mode = 'token'
              AND model_name.value = candidate.models->>0
        );
    END IF;

    IF v_claude_channel_id IS NOT NULL THEN
        INSERT INTO channel_model_pricing (
            channel_id, platform, billing_mode, models, input_price, output_price,
            cache_write_price, cache_read_price, image_output_price, per_request_price
        )
        SELECT v_claude_channel_id, 'anthropic', 'token', candidate.models,
               candidate.input_price, candidate.output_price, candidate.cache_write_price,
               candidate.cache_read_price, 0.00000000, NULL
        FROM (
            VALUES
                ('["claude-sonnet-4-5","claude-sonnet-4-5-20250929"]'::jsonb, 0.000003000000::numeric, 0.000015000000::numeric, 0.000003750000::numeric, 0.000000300000::numeric),
                ('["claude-fable-5","fable-max"]'::jsonb,                     0.000010000000::numeric, 0.000050000000::numeric, 0.000012500000::numeric, 0.000001000000::numeric)
        ) AS candidate(models, input_price, output_price, cache_write_price, cache_read_price)
        WHERE NOT EXISTS (
            SELECT 1
            FROM channel_model_pricing cmp
            CROSS JOIN LATERAL jsonb_array_elements_text(cmp.models) AS existing_model(value)
            CROSS JOIN LATERAL jsonb_array_elements_text(candidate.models) AS candidate_model(value)
            WHERE cmp.channel_id = v_claude_channel_id
              AND cmp.platform = 'anthropic'
              AND cmp.billing_mode = 'token'
              AND existing_model.value = candidate_model.value
        );
    END IF;

    UPDATE groups
    SET models_list_config = jsonb_set(
            CASE
                WHEN jsonb_typeof(COALESCE(models_list_config, '{}'::jsonb)->'models') = 'array'
                    THEN COALESCE(models_list_config, '{}'::jsonb)
                ELSE jsonb_set(COALESCE(models_list_config, '{}'::jsonb), '{models}', '[]'::jsonb, true)
            END,
            '{models}',
            (
                SELECT jsonb_agg(model_name ORDER BY sort_key, model_name)
                FROM (
                    SELECT DISTINCT model_name,
                           CASE
                               WHEN model_name LIKE 'gpt-5.6-%' THEN 0
                               ELSE 1
                           END AS sort_key
                    FROM (
                        SELECT jsonb_array_elements_text(
                            CASE
                                WHEN jsonb_typeof(COALESCE(groups.models_list_config, '{}'::jsonb)->'models') = 'array'
                                    THEN COALESCE(groups.models_list_config, '{}'::jsonb)->'models'
                                ELSE '[]'::jsonb
                            END
                        ) AS model_name
                        UNION ALL SELECT 'gpt-5.6-sol'
                        UNION ALL SELECT 'gpt-5.6-terra'
                        UNION ALL SELECT 'gpt-5.6-luna'
                    ) AS merged
                ) AS ordered
            ),
            true
        ),
        updated_at = NOW()
    WHERE platform = 'openai'
      AND deleted_at IS NULL
      AND name IN (
          'Codex team',
          'ChatGPT  Plus 官方订阅(月)',
          'Codex客户端（优化）',
          'Codex Pro（仅限Codex）',
          'Codex Pro（外接版）'
      )
      AND (
          models_list_config IS NULL
          OR NOT (COALESCE(COALESCE(models_list_config, '{}'::jsonb)->'models', '[]'::jsonb) ? 'gpt-5.6-sol')
          OR NOT (COALESCE(COALESCE(models_list_config, '{}'::jsonb)->'models', '[]'::jsonb) ? 'gpt-5.6-terra')
          OR NOT (COALESCE(COALESCE(models_list_config, '{}'::jsonb)->'models', '[]'::jsonb) ? 'gpt-5.6-luna')
      );
END $$;
