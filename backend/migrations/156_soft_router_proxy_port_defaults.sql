-- Move the soft-router defaults away from ports used by the original prototype.
--
-- Migration 155 has already shipped, so keep it immutable and adjust existing
-- untouched singleton config rows here.

ALTER TABLE soft_router_proxy_config
    ALTER COLUMN raw_port_start SET DEFAULT 12083,
    ALTER COLUMN public_port_start SET DEFAULT 1101,
    ALTER COLUMN public_port_end SET DEFAULT 1120;

UPDATE soft_router_proxy_config
SET raw_port_start = 12083,
    raw_port_end = 12150,
    public_port_start = 1101,
    public_port_end = 1120,
    updated_at = NOW()
WHERE id = 1
  AND raw_port_start = 12081
  AND raw_port_end = 12150
  AND public_port_start = 1081
  AND public_port_end = 1100;
