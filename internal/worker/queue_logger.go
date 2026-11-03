package worker

import (
	"fmt"
	"log/slog"
)

type queueLogger struct{ log *slog.Logger }

func (l queueLogger) Debug(args ...interface{}) {
	l.log.Debug(fmt.Sprint(args...), "module", "worker", "action", "asynq")
}
func (l queueLogger) Info(args ...interface{}) {
	l.log.Info(fmt.Sprint(args...), "module", "worker", "action", "asynq")
}
func (l queueLogger) Warn(args ...interface{}) {
	l.log.Warn(fmt.Sprint(args...), "module", "worker", "action", "asynq")
}
func (l queueLogger) Error(args ...interface{}) {
	l.log.Error(fmt.Sprint(args...), "module", "worker", "action", "asynq")
}
func (l queueLogger) Fatal(args ...interface{}) {
	l.log.Error(fmt.Sprint(args...), "module", "worker", "action", "asynq_fatal")
}
