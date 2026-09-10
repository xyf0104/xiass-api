ALTER TABLE pelican_benchmarks
    ADD COLUMN IF NOT EXISTS execution_node_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_id UUID,
    ADD COLUMN IF NOT EXISTS action VARCHAR(16) NOT NULL DEFAULT ''
        CHECK (action IN ('', 'continue', 'retry'));
