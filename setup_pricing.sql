-- ============================================================
-- 补充 Codex 和 Gemini 分组、渠道、模型定价
-- ============================================================

-- === Codex 分组 ===
INSERT INTO groups (name, description, rate_multiplier, platform, subscription_type, sort_order, is_exclusive, status, claude_code_only)
VALUES
  ('Codex Pro（仅限Codex）', 'ChatGPT 正价 Pro 号池，仅限在 Codex APP 和 Codex CLI 上使用，不支持外接', 0.6, 'openai', 'standard', 10, false, 'active', false),
  ('Codex Pro（外接版）', 'Codex Pro 外接版，支持第三方客户端接入', 0.7, 'openai', 'standard', 11, false, 'active', false);

-- === Gemini 分组 ===
INSERT INTO groups (name, description, rate_multiplier, platform, subscription_type, sort_order, is_exclusive, status, claude_code_only)
VALUES
  ('Gemini Pro（特价）', 'Google Gemini 低价渠道，适合日常文本生成和代码辅助', 0.5, 'gemini', 'standard', 20, false, 'active', false),
  ('Gemini Pro（标准）', 'Google Gemini 标准渠道，稳定可靠，全模型支持', 0.8, 'gemini', 'standard', 21, false, 'active', false);

-- === Codex 渠道 ===
INSERT INTO channels (name, description, status)
VALUES ('Codex 号池', 'OpenAI Codex/GPT 全系列模型，支持 GPT-5.5 / GPT-5.4 等', 'active');

-- === Gemini 渠道 ===
INSERT INTO channels (name, description, status)
VALUES ('Gemini 号池', 'Google Gemini 全系列模型，支持 Gemini 2.5 Pro / Flash 等', 'active');

-- 获取新创建的渠道和分组 ID
-- 假设 Codex 渠道 id=2, Gemini 渠道 id=3
-- 假设 Codex 分组 id=6,7, Gemini 分组 id=8,9

-- === Codex 渠道模型定价 ===
-- GPT-5.5: 官方 $5 input / $30 output per 1M tokens
INSERT INTO channel_model_pricing (channel_id, models, input_price, output_price, cache_write_price, cache_read_price, image_output_price, billing_mode, platform)
SELECT c.id, '["gpt-5.5"]', 0.000005, 0.000030, 0, 0, 0, 'token', 'openai'
FROM channels c WHERE c.name = 'Codex 号池';

-- GPT-5.4: 官方 $2.5 input / $15 output
INSERT INTO channel_model_pricing (channel_id, models, input_price, output_price, cache_write_price, cache_read_price, image_output_price, billing_mode, platform)
SELECT c.id, '["gpt-5.4"]', 0.0000025, 0.000015, 0, 0, 0, 'token', 'openai'
FROM channels c WHERE c.name = 'Codex 号池';

-- GPT-5.4-mini: 官方 $0.75 input / $4.5 output
INSERT INTO channel_model_pricing (channel_id, models, input_price, output_price, cache_write_price, cache_read_price, image_output_price, billing_mode, platform)
SELECT c.id, '["gpt-5.4-mini"]', 0.00000075, 0.0000045, 0, 0, 0, 'token', 'openai'
FROM channels c WHERE c.name = 'Codex 号池';

-- === Gemini 渠道模型定价 ===
-- Gemini 2.5 Pro: 官方 $1.25 input / $10 output per 1M
INSERT INTO channel_model_pricing (channel_id, models, input_price, output_price, cache_write_price, cache_read_price, image_output_price, billing_mode, platform)
SELECT c.id, '["gemini-2.5-pro"]', 0.00000125, 0.000010, 0.000000315, 0.00000031, 0, 'token', 'gemini'
FROM channels c WHERE c.name = 'Gemini 号池';

-- Gemini 2.5 Flash: 官方 $0.15 input / $3.5 output
INSERT INTO channel_model_pricing (channel_id, models, input_price, output_price, cache_write_price, cache_read_price, image_output_price, billing_mode, platform)
SELECT c.id, '["gemini-2.5-flash"]', 0.00000015, 0.0000035, 0.0000000375, 0.000000015, 0, 'token', 'gemini'
FROM channels c WHERE c.name = 'Gemini 号池';

-- Gemini 2.0 Flash: 更便宜
INSERT INTO channel_model_pricing (channel_id, models, input_price, output_price, cache_write_price, cache_read_price, image_output_price, billing_mode, platform)
SELECT c.id, '["gemini-2.0-flash"]', 0.0000001, 0.000002, 0, 0, 0, 'token', 'gemini'
FROM channels c WHERE c.name = 'Gemini 号池';

-- === 绑定分组到渠道 ===
-- Codex 分组绑定到 Codex 渠道
INSERT INTO channel_groups (channel_id, group_id)
SELECT c.id, g.id FROM channels c, groups g
WHERE c.name = 'Codex 号池' AND g.name IN ('Codex Pro（仅限Codex）', 'Codex Pro（外接版）');

-- Gemini 分组绑定到 Gemini 渠道
INSERT INTO channel_groups (channel_id, group_id)
SELECT c.id, g.id FROM channels c, groups g
WHERE c.name = 'Gemini 号池' AND g.name IN ('Gemini Pro（特价）', 'Gemini Pro（标准）');

-- === 验证所有数据 ===
SELECT '=== 所有分组 ===' AS info;
SELECT id, name, platform, rate_multiplier, sort_order FROM groups WHERE deleted_at IS NULL ORDER BY sort_order;

SELECT '=== 所有渠道 ===' AS info;
SELECT id, name, description FROM channels WHERE status = 'active';

SELECT '=== 渠道-分组绑定 ===' AS info;
SELECT c.name AS channel, g.name AS group_name, g.rate_multiplier
FROM channel_groups cg
JOIN channels c ON c.id = cg.channel_id
JOIN groups g ON g.id = cg.group_id
ORDER BY c.id, g.sort_order;

SELECT '=== 模型定价 ===' AS info;
SELECT c.name AS channel, cmp.models, cmp.input_price, cmp.output_price, cmp.platform
FROM channel_model_pricing cmp
JOIN channels c ON c.id = cmp.channel_id
ORDER BY c.id, cmp.id;
