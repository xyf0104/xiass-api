CREATE TABLE IF NOT EXISTS pelican_benchmarks (
    id UUID PRIMARY KEY,
    batch_id UUID NOT NULL,
    account_id BIGINT NOT NULL,
    account_name TEXT NOT NULL,
    model VARCHAR(200) NOT NULL,
    upstream_model VARCHAR(200) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','canceling','succeeded','failed','canceled','interrupted')),
    executor_id UUID,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT,
    html TEXT NOT NULL DEFAULT '' CHECK (octet_length(html) <= 1048576),
    CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

-- Cancellation is a request, not acknowledgement that upstream has exited.
CREATE UNIQUE INDEX IF NOT EXISTS pelican_benchmarks_active_account
    ON pelican_benchmarks (account_id) WHERE status IN ('queued','running','canceling');
CREATE INDEX IF NOT EXISTS pelican_benchmarks_created ON pelican_benchmarks (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS pelican_benchmarks_batch ON pelican_benchmarks (batch_id, created_at DESC);
CREATE INDEX IF NOT EXISTS pelican_benchmarks_account ON pelican_benchmarks (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS pelican_benchmarks_queue ON pelican_benchmarks (created_at, id) WHERE status = 'queued';
