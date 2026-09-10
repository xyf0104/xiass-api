CREATE INDEX IF NOT EXISTS pelican_benchmarks_retention
    ON pelican_benchmarks (finished_at, id)
    WHERE status IN ('succeeded','failed','canceled','interrupted') AND html <> '';
CREATE INDEX IF NOT EXISTS pelican_benchmarks_active_source
    ON pelican_benchmarks (source_id)
    WHERE source_id IS NOT NULL AND status IN ('queued','running','canceling');
