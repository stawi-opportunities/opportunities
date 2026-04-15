package queue

import (
	"log/slog"

	"stawi.jobs/internal/config"
)

func New(cfg config.Config, log *slog.Logger) Queue {
	if cfg.RedisAddr == "" {
		log.Warn("REDIS_ADDR empty, using memory queue")
		return NewMemoryQueue(2048)
	}
	return NewRedis(cfg.RedisAddr, cfg.QueueName)
}
