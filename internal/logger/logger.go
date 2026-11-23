package logger

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

// Logger wraps logrus logger and implements the install.Logger interface.
type Logger struct {
	*logrus.Logger
}

// NewLogger creates a new logger.
// Diagnostic output goes to stderr so machine-readable stdout (JSON) stays pipe-safe.
func NewLogger(level string, logFile string) (*Logger, error) {
	log := logrus.New()

	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logLevel = logrus.InfoLevel
	}
	log.SetLevel(logLevel)

	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	writers := []io.Writer{os.Stderr}
	if logFile != "" {
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, err
		}
		writers = append(writers, file)
	}

	log.SetOutput(io.MultiWriter(writers...))

	return &Logger{Logger: log}, nil
}
