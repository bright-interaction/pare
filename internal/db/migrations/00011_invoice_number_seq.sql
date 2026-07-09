-- +goose Up
-- +goose StatementBegin
-- Per-company, per-fiscal-year invoice number counter. Allocated inside the
-- finalize transaction via an upsert-returning so numbering is gap-free and
-- race-safe (concurrent finalizes serialize on the row lock) and resets to 0001
-- each year. Replaces the old full-table COUNT, which broke at year rollover and
-- could mint duplicate numbers.
CREATE TABLE invoice_number_seq (
    company_id UUID   NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    year       INT    NOT NULL,
    next_no    BIGINT NOT NULL,
    PRIMARY KEY (company_id, year)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS invoice_number_seq;
-- +goose StatementEnd
