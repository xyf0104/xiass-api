-- Seed default channel/group/model pricing for fresh deployments.
--
-- The customer pricing page and the admin channel-pricing page are backed by
-- channels, channel_groups, and channel_model_pricing.  Fresh installs used to
-- create the schema but not the default business data, leaving both pages empty.
--
-- Guardrail: only seed when the instance has no channel_model_pricing rows at
-- all.  Existing deployments that already configured pricing are left untouched.

DO $$
DECLARE
    v_kiro_channel_id BIGINT;
    v_codex_channel_id BIGINT;
    v_claude_channel_id BIGINT;
    v_gemini_channel_id BIGINT;
    v_antigravity_channel_id BIGINT;
    v_image_pricing_id BIGINT;
BEGIN
    IF EXISTS (SELECT 1 FROM channel_model_pricing LIMIT 1) THEN
        RAISE NOTICE 'default channel pricing seed skipped: channel_model_pricing is not empty';
        RETURN;
    END IF;

    -- Channels
    INSERT INTO channels (
        name, description, status, model_mapping, billing_model_source,
        restrict_models, features, apply_pricing_to_account_stats, features_config
    )
    VALUES
        ('kiro号池', 'Anthropic Claude 官方 API，支持 Opus/Sonnet 全系列模型', 'active', '{}'::jsonb, 'upstream', false, '', true, '{}'::jsonb),
        ('Codex 号池', 'OpenAI Codex/GPT 全系列模型，支持 GPT-5.5 / GPT-5.4 等', 'active', '{}'::jsonb, 'upstream', false, '', true, '{"codex_image_generation_bridge":{"openai":true}}'::jsonb),
        ('jojo-max-claude', '', 'active', '{"anthropic":{"kiro-4-8":"claude-opus-4-8","opus-max":"claude-opus-4-8","fable-max":"claude-fable-5"}}'::jsonb, 'upstream', false, '', false, '{"bedrock_cc_compat":{"anthropic":false},"web_search_emulation":{"anthropic":false}}'::jsonb),
        ('Gemini 渠道', 'Google Gemini OAuth 模型渠道', 'active', '{}'::jsonb, 'upstream', false, '', true, '{}'::jsonb),
        ('Antigravity 渠道', 'Antigravity 混合模型渠道（Claude + Gemini）', 'active', '{}'::jsonb, 'upstream', false, '', true, '{}'::jsonb)
    ON CONFLICT (name) DO UPDATE SET
        description = EXCLUDED.description,
        status = EXCLUDED.status,
        model_mapping = EXCLUDED.model_mapping,
        billing_model_source = EXCLUDED.billing_model_source,
        restrict_models = EXCLUDED.restrict_models,
        features = EXCLUDED.features,
        apply_pricing_to_account_stats = EXCLUDED.apply_pricing_to_account_stats,
        features_config = EXCLUDED.features_config,
        updated_at = NOW();

    SELECT id INTO v_kiro_channel_id FROM channels WHERE name = 'kiro号池';
    SELECT id INTO v_codex_channel_id FROM channels WHERE name = 'Codex 号池';
    SELECT id INTO v_claude_channel_id FROM channels WHERE name = 'jojo-max-claude';
    SELECT id INTO v_gemini_channel_id FROM channels WHERE name = 'Gemini 渠道';
    SELECT id INTO v_antigravity_channel_id FROM channels WHERE name = 'Antigravity 渠道';

    -- Groups.  These are the public/default groups used by the seeded channels.
    INSERT INTO groups (
        name, description, rate_multiplier, cost_ratio, platform, subscription_type,
        sort_order, is_exclusive, status, claude_code_only, supported_model_scopes,
        allow_image_generation, image_rate_independent, image_rate_multiplier,
        models_list_config, rpm_limit, require_oauth_only, require_privacy_set,
        allow_messages_dispatch, default_mapped_model, messages_dispatch_model_config
    )
    VALUES
        ('Codex team', 'Codex team 专线，极致性价比的 OpenAI 官方模型号池，支持全渠道调用。', 0.2500, 0.1900, 'openai', 'standard', 0, false, 'active', false, '[]'::jsonb, true, true, 1.0000, '{"models":["gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex","gpt-5.3-codex-spark","codex-auto-review","gpt-5.2","gpt-image-1","gpt-image-1.5","gpt-image-2"],"enabled":false}'::jsonb, 0, false, false, false, '', '{"opus_mapped_model":"gpt-5.4","haiku_mapped_model":"gpt-5.4-mini","sonnet_mapped_model":"gpt-5.3-codex"}'::jsonb),
        ('ChatGPT  Plus 官方订阅(月)', '官方20美金 ChatGPT Plus订阅账号 30天有效期 每日限额', 0.4000, NULL, 'openai', 'subscription', 0, true, 'active', false, '[]'::jsonb, true, false, 1.0000, '{"models":["gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex","gpt-5.3-codex-spark","codex-auto-review","gpt-5.2","gpt-image-1","gpt-image-1.5","gpt-image-2"],"enabled":false}'::jsonb, 0, false, false, false, '', '{"opus_mapped_model":"gpt-5.4","haiku_mapped_model":"gpt-5.4-mini","sonnet_mapped_model":"gpt-5.3-codex"}'::jsonb),
        ('Codex客户端（优化）', 'ChatGPT 正价 Pro 号池，仅限在 Codex APP 和 Codex cli上使用', 0.6000, 0.5000, 'openai', 'standard', 0, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex","gpt-5.3-codex-spark","codex-auto-review","gpt-5.2","gpt-image-1","gpt-image-1.5","gpt-image-2"],"enabled":false}'::jsonb, 0, false, false, false, '', '{"opus_mapped_model":"gpt-5.4","haiku_mapped_model":"gpt-5.4-mini","sonnet_mapped_model":"gpt-5.3-codex"}'::jsonb),
        ('Codex Pro（仅限Codex）', 'ChatGPT 官方正价 Pro 号池，专供 Codex APP 与 CLI 端使用，不支持第三方客户端外接。', 0.5000, 0.2800, 'openai', 'standard', 10, false, 'active', false, '[]'::jsonb, true, false, 1.0000, '{"models":["gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex","gpt-5.3-codex-spark","codex-auto-review","gpt-5.2","gpt-image-1","gpt-image-1.5","gpt-image-2"],"enabled":false}'::jsonb, 0, false, false, false, '', '{"opus_mapped_model":"gpt-5.4","haiku_mapped_model":"gpt-5.4-mini","sonnet_mapped_model":"gpt-5.3-codex"}'::jsonb),
        ('Codex Pro（外接版）', 'Codex Pro 专属外接通道，极速体验，支持所有第三方客户端接入 OpenAI 系列模型。', 0.8000, 0.7000, 'openai', 'standard', 11, false, 'active', false, '[]'::jsonb, true, false, 1.0000, '{"models":["gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex","gpt-5.3-codex-spark","codex-auto-review","gpt-5.2","gpt-image-1","gpt-image-1.5","gpt-image-2"],"enabled":false}'::jsonb, 0, false, false, false, '', '{"opus_mapped_model":"gpt-5.4","haiku_mapped_model":"gpt-5.4-mini","sonnet_mapped_model":"gpt-5.3-codex"}'::jsonb),
        ('Claude Kiro', '基于 Kiro 高速通道的高性价比 Opus 模型，支持各大第三方客户端接入。', 0.5000, 0.2800, 'anthropic', 'standard', 0, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["claude-fable-5","claude-opus-4-5-20251101","claude-opus-4-6","claude-opus-4-7","claude-opus-4-8","claude-sonnet-4-6","claude-sonnet-4-5-20250929","claude-haiku-4-5-20251001"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('Claude Plus (精品）', 'Claude 更加优质的第三方渠道，可平替官方 Max，建议在特价分组不稳定时切换使用', 1.2000, 1.0000, 'anthropic', 'standard', 0, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["claude-fable-5","claude-opus-4-5-20251101","claude-opus-4-6","claude-opus-4-7","claude-opus-4-8","claude-sonnet-4-6","claude-sonnet-4-5-20250929","claude-haiku-4-5-20251001"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('测试', '', 0.0100, 0.0700, 'anthropic', 'standard', 0, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["claude-opus-4-5-20251101","claude-opus-4-6","claude-opus-4-7","claude-opus-4-8","claude-sonnet-4-6","claude-sonnet-4-5-20250929","claude-haiku-4-5-20251001"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('Claude 标准（特价）', 'Claude 官方标准渠道，原汁原味稳定可靠，支持 Opus 和 Sonnet 全系列模型。', 0.8000, 0.7000, 'anthropic', 'standard', 2, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["claude-fable-5","claude-opus-4-5-20251101","claude-opus-4-6","claude-opus-4-7","claude-opus-4-8","claude-sonnet-4-6","claude-sonnet-4-5-20250929","claude-haiku-4-5-20251001","claude-3-7-sonnet-20250219","claude-3-5-sonnet-20240620","claude-opus-4-1-20250805","claude-sonnet-4-20250514","claude-3-5-haiku-20241022","claude-3-5-sonnet-20241022","claude-opus-4-20250514"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('Claude Max（仅限CC）', '专为 Claude Code 客户端打造的高性能编程通道，满血极速输出，不支持第三方外接。', 1.9000, 1.8000, 'anthropic', 'standard', 3, false, 'active', true, '[]'::jsonb, false, false, 1.0000, '{"models":["claude-fable-5","claude-opus-4-5-20251101","claude-opus-4-6","claude-opus-4-7","claude-opus-4-8","claude-sonnet-4-6","claude-sonnet-4-5-20250929","claude-haiku-4-5-20251001","claude-3-7-sonnet-20250219","claude-3-5-haiku-20241022","claude-3-5-sonnet-20240620","claude-3-5-sonnet-20241022","claude-sonnet-4-20250514","claude-opus-4-20250514","claude-opus-4-1-20250805"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('Claude Max（外接版）', '全渠道通用的满血 Claude Max，支持所有第三方客户端稳定接入，无任何使用限制。', 2.2000, 2.0000, 'anthropic', 'standard', 4, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["claude-opus-4-6","claude-opus-4-7","claude-opus-4-8"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('gemini', '', 0.3000, NULL, 'gemini', 'standard', 0, false, 'active', false, '[]'::jsonb, false, false, 1.0000, '{"models":["gemini-2.0-flash","gemini-2.5-flash","gemini-2.5-flash-image","gemini-2.5-pro","gemini-3.5-flash","gemini-3-flash-preview","gemini-3-pro-preview","gemini-3.1-pro-preview","gemini-3.1-flash-image"],"enabled":false}'::jsonb, 0, false, false, false, '', '{}'::jsonb),
        ('Antigravity', 'Antigravity 平台，支持 Claude/Gemini 混合模型', 1.0000, NULL, 'antigravity', 'standard', 0, false, 'active', false, '["claude","gemini_text","gemini_image"]'::jsonb, false, false, 1.0000, '{}'::jsonb, 0, false, false, false, '', '{}'::jsonb)
    ON CONFLICT (name) WHERE deleted_at IS NULL DO UPDATE SET
        description = EXCLUDED.description,
        rate_multiplier = EXCLUDED.rate_multiplier,
        cost_ratio = EXCLUDED.cost_ratio,
        platform = EXCLUDED.platform,
        subscription_type = EXCLUDED.subscription_type,
        sort_order = EXCLUDED.sort_order,
        is_exclusive = EXCLUDED.is_exclusive,
        status = EXCLUDED.status,
        claude_code_only = EXCLUDED.claude_code_only,
        supported_model_scopes = EXCLUDED.supported_model_scopes,
        allow_image_generation = EXCLUDED.allow_image_generation,
        image_rate_independent = EXCLUDED.image_rate_independent,
        image_rate_multiplier = EXCLUDED.image_rate_multiplier,
        models_list_config = EXCLUDED.models_list_config,
        rpm_limit = EXCLUDED.rpm_limit,
        require_oauth_only = EXCLUDED.require_oauth_only,
        require_privacy_set = EXCLUDED.require_privacy_set,
        allow_messages_dispatch = EXCLUDED.allow_messages_dispatch,
        default_mapped_model = EXCLUDED.default_mapped_model,
        messages_dispatch_model_config = EXCLUDED.messages_dispatch_model_config,
        deleted_at = NULL,
        updated_at = NOW();

    -- Channel/group bindings.
    INSERT INTO channel_groups (channel_id, group_id)
    SELECT v_codex_channel_id, g.id
    FROM groups g
    WHERE g.name IN (
        'Codex team',
        'ChatGPT  Plus 官方订阅(月)',
        'Codex客户端（优化）',
        'Codex Pro（仅限Codex）',
        'Codex Pro（外接版）'
    )
    ON CONFLICT (group_id) DO UPDATE SET channel_id = EXCLUDED.channel_id;

    INSERT INTO channel_groups (channel_id, group_id)
    SELECT v_claude_channel_id, g.id
    FROM groups g
    WHERE g.name IN (
        'Claude Kiro',
        'Claude Plus (精品）',
        '测试',
        'Claude 标准（特价）',
        'Claude Max（仅限CC）',
        'Claude Max（外接版）'
    )
    ON CONFLICT (group_id) DO UPDATE SET channel_id = EXCLUDED.channel_id;

    INSERT INTO channel_groups (channel_id, group_id)
    SELECT v_gemini_channel_id, g.id
    FROM groups g
    WHERE g.name = 'gemini'
    ON CONFLICT (group_id) DO UPDATE SET channel_id = EXCLUDED.channel_id;

    INSERT INTO channel_groups (channel_id, group_id)
    SELECT v_antigravity_channel_id, g.id
    FROM groups g
    WHERE g.name = 'Antigravity'
    ON CONFLICT (group_id) DO UPDATE SET channel_id = EXCLUDED.channel_id;

    -- Model pricing.
    INSERT INTO channel_model_pricing (
        channel_id, platform, billing_mode, models, input_price, output_price,
        cache_write_price, cache_read_price, image_output_price, per_request_price
    )
    VALUES
        (v_codex_channel_id, 'openai', 'token', '["gpt-5.5"]'::jsonb, 0.000005000000, 0.000030000000, 0.000004000000, 0.000000500000, 0.00000000, NULL),
        (v_codex_channel_id, 'openai', 'token', '["gpt-5.4"]'::jsonb, 0.000002500000, 0.000015000000, 0.000002000000, 0.000000250000, 0.00000000, NULL),
        (v_codex_channel_id, 'openai', 'token', '["gpt-5.4-mini"]'::jsonb, 0.000000750000, 0.000004500000, 0.000000600000, 0.000000075000, 0.00000000, NULL),
        (v_codex_channel_id, 'openai', 'image', '["gpt-image-2"]'::jsonb, 0.000005000000, 0.000010000000, 0.000000000000, 0.000001250000, 0.00003000, 0.3000000000),
        (v_claude_channel_id, 'anthropic', 'token', '["claude-opus-4-8","claude-opus-4-7","claude-opus-4-6","opus-max","kiro-4-8"]'::jsonb, 0.000005000000, 0.000025000000, 0.000006250000, 0.000000500000, 0.00000000, NULL),
        (v_claude_channel_id, 'anthropic', 'token', '["claude-sonnet-4-6"]'::jsonb, 0.000004000000, 0.000020000000, 0.000005000000, 0.000000400000, 0.00000000, NULL),
        (v_claude_channel_id, 'anthropic', 'token', '["claude-fable-5","fable-max"]'::jsonb, 0.000010000000, 0.000050000000, 0.000012500000, 0.000001000000, 0.00000000, NULL),
        (v_gemini_channel_id, 'gemini', 'token', '["gemini-2.5-flash"]'::jsonb, 0.000001000000, 0.000004000000, NULL, NULL, NULL, NULL),
        (v_gemini_channel_id, 'gemini', 'token', '["gemini-2.5-pro"]'::jsonb, 0.000002000000, 0.000010000000, NULL, NULL, NULL, NULL),
        (v_antigravity_channel_id, 'antigravity', 'token', '["claude-sonnet-4-6-thinking","claude-sonnet-4-6"]'::jsonb, 0.000004000000, 0.000020000000, NULL, NULL, NULL, NULL),
        (v_antigravity_channel_id, 'antigravity', 'token', '["claude-opus-4-6-thinking","claude-opus-4-6"]'::jsonb, 0.000005000000, 0.000025000000, NULL, NULL, NULL, NULL),
        (v_antigravity_channel_id, 'antigravity', 'token', '["gemini-3.5-flash-medium","gemini-3.5-flash-high","gemini-3.5-flash-low","gemini-3.5-flash"]'::jsonb, 0.000001000000, 0.000004000000, NULL, NULL, NULL, NULL),
        (v_antigravity_channel_id, 'antigravity', 'token', '["gemini-3.1-pro-high","gemini-3.1-pro-low"]'::jsonb, 0.000002000000, 0.000010000000, NULL, NULL, NULL, NULL),
        (v_antigravity_channel_id, 'antigravity', 'token', '["gpt-oss-120b-medium"]'::jsonb, 0.000003000000, 0.000015000000, NULL, NULL, NULL, NULL);

    SELECT id INTO v_image_pricing_id
    FROM channel_model_pricing
    WHERE channel_id = v_codex_channel_id
      AND platform = 'openai'
      AND billing_mode = 'image'
      AND models = '["gpt-image-2"]'::jsonb
    ORDER BY id DESC
    LIMIT 1;

    INSERT INTO channel_pricing_intervals (
        pricing_id, min_tokens, max_tokens, tier_label, input_price, output_price,
        cache_write_price, cache_read_price, per_request_price, sort_order
    )
    VALUES
        (v_image_pricing_id, 0, NULL, '1K', NULL, NULL, NULL, NULL, 0.100000000000, 0),
        (v_image_pricing_id, 0, NULL, '2K', NULL, NULL, NULL, NULL, 0.200000000000, 1),
        (v_image_pricing_id, 0, NULL, '4K', NULL, NULL, NULL, NULL, 0.400000000000, 2);

    RAISE NOTICE 'default channel pricing seed applied';
END $$;
