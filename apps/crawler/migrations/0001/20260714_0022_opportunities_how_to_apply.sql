-- Paywalled application instructions, stored separately from the public
-- description so free users never receive how-to-apply content on the
-- public jobs API. GORM AutoMigrate also adds this column; this file
-- keeps capability/cluster deploys that only run SQL migrations aligned.

ALTER TABLE opportunities
    ADD COLUMN IF NOT EXISTS how_to_apply text;
