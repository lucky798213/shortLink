package logging

import (
	"log/slog"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
	"go.uber.org/zap/zapcore"
)

var (
	globalMu sync.RWMutex
	global   = zap.NewNop()
)

func Init(level string, development bool) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	if development {
		cfg = zap.NewDevelopmentConfig()
		cfg.Level = zap.NewAtomicLevelAt(zapLevel)
	}

	logger, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	Set(logger)
	return logger, nil
}

func Set(logger *zap.Logger) {
	if logger == nil {
		logger = zap.NewNop()
	}
	globalMu.Lock()
	global = logger
	globalMu.Unlock()
	slog.SetDefault(slog.New(zapslog.NewHandler(logger.Core())))
}

func L() *zap.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

func Sync() {
	_ = L().Sync()
}

func init() {
	logger := zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(os.Stdout),
		zapcore.InfoLevel,
	))
	Set(logger)
}
