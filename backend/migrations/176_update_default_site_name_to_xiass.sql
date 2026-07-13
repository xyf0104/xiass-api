UPDATE settings
SET value = 'XIASS API',
    updated_at = NOW()
WHERE key = 'site_name'
  AND value = 'NoWind API';
