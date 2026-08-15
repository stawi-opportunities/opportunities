# ATS migrations

Schema is applied via Frame `repository.Migrate` (GORM AutoMigrate of `models.Schema()`).

SQL files in this directory are reserved for manual index/data backfills that AutoMigrate cannot express.
