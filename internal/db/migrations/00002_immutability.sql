-- +goose Up
-- +goose StatementBegin
-- Verifikat immutability (Bokforingslagen): once a verification is posted it and
-- its lines can never be updated or deleted. Corrections are new verifications
-- (rattelseverifikat) via reversal_of. Inserts stay allowed so posting can write
-- the parent and its lines in one transaction.
CREATE OR REPLACE FUNCTION pare_block_posted_verification() RETURNS trigger AS $$
BEGIN
    IF OLD.posted_at IS NOT NULL THEN
        RAISE EXCEPTION 'verification % is posted and immutable', OLD.id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_verifications_immutable
    BEFORE UPDATE OR DELETE ON verifications
    FOR EACH ROW EXECUTE FUNCTION pare_block_posted_verification();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION pare_block_posted_line() RETURNS trigger AS $$
DECLARE posted TIMESTAMPTZ;
BEGIN
    SELECT posted_at INTO posted FROM verifications
        WHERE id = COALESCE(OLD.verification_id, NEW.verification_id);
    IF posted IS NOT NULL THEN
        RAISE EXCEPTION 'verification % is posted; its lines are immutable',
            COALESCE(OLD.verification_id, NEW.verification_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_vlines_immutable
    BEFORE UPDATE OR DELETE ON verification_lines
    FOR EACH ROW EXECUTE FUNCTION pare_block_posted_line();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_vlines_immutable ON verification_lines;
DROP TRIGGER IF EXISTS trg_verifications_immutable ON verifications;
DROP FUNCTION IF EXISTS pare_block_posted_line();
DROP FUNCTION IF EXISTS pare_block_posted_verification();
-- +goose StatementEnd
