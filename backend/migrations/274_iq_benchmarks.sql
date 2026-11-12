-- Preserve existing HTML tests and their history. Empty historical prompts
-- resolve to the unchanged original Pelican prompt in the application.
ALTER TABLE pelican_benchmarks
    ADD COLUMN IF NOT EXISTS test_type VARCHAR(16) NOT NULL DEFAULT 'pelican'
        CHECK (test_type IN ('pelican','candy')),
    ADD COLUMN IF NOT EXISTS prompt TEXT NOT NULL DEFAULT ''
        CHECK (char_length(prompt) <= 8000 AND octet_length(prompt) <= 32000),
    ADD COLUMN IF NOT EXISTS text_output TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(text_output) <= 1048576),
    ADD COLUMN IF NOT EXISTS answer INTEGER,
    ADD COLUMN IF NOT EXISTS passed BOOLEAN;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pelican_benchmarks'::regclass AND conname = 'pelican_benchmarks_candy_output') THEN
        ALTER TABLE pelican_benchmarks ADD CONSTRAINT pelican_benchmarks_candy_output
            CHECK (test_type <> 'candy' OR html = '');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pelican_benchmarks'::regclass AND conname = 'pelican_benchmarks_candy_no_continuation') THEN
        ALTER TABLE pelican_benchmarks ADD CONSTRAINT pelican_benchmarks_candy_no_continuation
            CHECK (test_type <> 'candy' OR action <> 'continue');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pelican_benchmarks'::regclass AND conname = 'pelican_benchmarks_grading') THEN
        ALTER TABLE pelican_benchmarks ADD CONSTRAINT pelican_benchmarks_grading CHECK (
            (answer IS NULL AND passed IS NULL) OR (test_type = 'candy' AND status = 'succeeded' AND passed IS NOT NULL)
        );
    END IF;
END $$;

-- Different tests may execute in parallel for one account. Duplicate tests
-- remain guarded across processes, including until cancellation is acknowledged.
CREATE UNIQUE INDEX IF NOT EXISTS pelican_benchmarks_active_account_test
    ON pelican_benchmarks (account_id, test_type)
    WHERE status IN ('queued','running','canceling');
DROP INDEX IF EXISTS pelican_benchmarks_active_account;

CREATE INDEX IF NOT EXISTS pelican_benchmarks_output_retention
    ON pelican_benchmarks (finished_at, id)
    WHERE status IN ('succeeded','failed','canceled','interrupted') AND (html <> '' OR text_output <> '');
DROP INDEX IF EXISTS pelican_benchmarks_retention;
