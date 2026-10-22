package policy

import (
	"io"

	"github.com/sirupsen/logrus"
)

func discardLogger() *logrus.Logger {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}
