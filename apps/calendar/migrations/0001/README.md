# Calendar migrations

Schema is applied via Frame `Migrate` + `models.Schema()` (GORM AutoMigrate) on the setup Job.

Tables: `cal_resources`, `cal_availability`, `cal_busy_blocks`, `cal_bookings`, `cal_booking_lines`, `cal_external_connections`, `cal_sync_outbox`.
