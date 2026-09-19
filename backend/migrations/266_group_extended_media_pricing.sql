-- Additive persistence for model-specific video, search, and voice pricing.
-- NULL keeps the existing code-default fallback; explicit zero means free.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS video_model_prices JSONB,
    ADD COLUMN IF NOT EXISTS search_price_per_1k DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS audio_realtime_price_per_min DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS audio_tts_price_per_million_chars DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS audio_stt_price_per_hour DECIMAL(20,8);

COMMENT ON COLUMN groups.video_model_prices IS
    '可选：按模型族和分辨率覆盖视频每秒单价（USD/s）；NULL 或空对象回退到 video_price_* 或代码默认';
COMMENT ON COLUMN groups.search_price_per_1k IS
    '搜索工具每 1000 次调用价格（USD）；NULL 使用代码默认，0 表示免费';
COMMENT ON COLUMN groups.audio_realtime_price_per_min IS
    'Voice Realtime 每分钟价格（USD）；NULL 使用代码默认，0 表示免费';
COMMENT ON COLUMN groups.audio_tts_price_per_million_chars IS
    'TTS 每百万字符价格（USD）；NULL 使用代码默认，0 表示免费';
COMMENT ON COLUMN groups.audio_stt_price_per_hour IS
    'STT 每小时价格（USD）；NULL 使用代码默认，0 表示免费';
