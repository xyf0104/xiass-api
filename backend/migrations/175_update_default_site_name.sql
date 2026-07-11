UPDATE settings
SET value = 'NoWind API',
    updated_at = NOW()
WHERE key = 'site_name'
  AND value = 'Sub2API';
