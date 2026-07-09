-- +goose Up
-- +goose StatementBegin
-- Make fiscal years first-class: a year can be closed (bokslut), which posts the
-- result to 2099 and locks the period. closed_at marks it done.
ALTER TABLE fiscal_years ADD COLUMN closed_at TIMESTAMPTZ;
CREATE UNIQUE INDEX idx_fiscal_years_company_start ON fiscal_years (company_id, starts_on);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_fiscal_years_company_start;
ALTER TABLE fiscal_years DROP COLUMN IF EXISTS closed_at;
-- +goose StatementEnd
