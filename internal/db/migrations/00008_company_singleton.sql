-- +goose Up
-- +goose StatementBegin
-- V1 is single-company. A unique index on a constant makes a second company
-- impossible, so a concurrent /setup race (two bootstraps) can create at most
-- one company (the loser fails before creating a user). Drop this when
-- multi-company (pro) lands.
CREATE UNIQUE INDEX one_company ON companies ((true));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS one_company;
-- +goose StatementEnd
