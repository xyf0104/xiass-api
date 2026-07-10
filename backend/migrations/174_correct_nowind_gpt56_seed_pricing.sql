-- Correct the GPT-5.6 prices inserted by the original v1.0.61 NoWind seed.
--
-- The previous seed used the Sol prices for Sol, Terra, and Luna and used an
-- incorrect cache-write price. Only rows that still exactly match that seed
-- are changed, so administrator-customized pricing remains untouched.

UPDATE channel_model_pricing AS cmp
SET input_price = corrected.input_price,
    output_price = corrected.output_price,
    cache_write_price = corrected.cache_write_price,
    cache_read_price = corrected.cache_read_price,
    updated_at = NOW()
FROM (
    VALUES
        ('gpt-5.6-sol',   0.000005000000::numeric, 0.000030000000::numeric, 0.000006250000::numeric, 0.000000500000::numeric),
        ('gpt-5.6-terra', 0.000002500000::numeric, 0.000015000000::numeric, 0.000003125000::numeric, 0.000000250000::numeric),
        ('gpt-5.6-luna',  0.000001000000::numeric, 0.000006000000::numeric, 0.000001250000::numeric, 0.000000100000::numeric)
) AS corrected(model_name, input_price, output_price, cache_write_price, cache_read_price)
WHERE cmp.platform = 'openai'
  AND cmp.billing_mode = 'token'
  AND cmp.models = jsonb_build_array(corrected.model_name)
  AND cmp.input_price = 0.000005000000::numeric
  AND cmp.output_price = 0.000030000000::numeric
  AND cmp.cache_write_price = 0.000004000000::numeric
  AND cmp.cache_read_price = 0.000000500000::numeric
  AND EXISTS (
      SELECT 1
      FROM channels AS c
      WHERE c.id = cmp.channel_id
        AND c.name = 'Codex 号池'
  );
