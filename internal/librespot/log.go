package librespot

import (
	"fmt"
	"os"
	"strings"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/sirupsen/logrus"
)

type LogrusAdapter struct {
	Log *logrus.Entry
}

var authKeywords = []string{
	"complete authentication",
	"visit the following link",
}

func stderrIfAuth(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, kw := range authKeywords {
		if strings.Contains(strings.ToLower(msg), kw) {
			fmt.Fprintln(os.Stderr, "\n[orpheus] "+msg)
			return
		}
	}
}

func (l LogrusAdapter) Tracef(format string, args ...any) { l.Log.Tracef(format, args...) }
func (l LogrusAdapter) Debugf(format string, args ...any) { l.Log.Debugf(format, args...) }
func (l LogrusAdapter) Infof(format string, args ...any) {
	stderrIfAuth(format, args...)
	l.Log.Infof(format, args...)
}
func (l LogrusAdapter) Warnf(format string, args ...any)  { l.Log.Warnf(format, args...) }
func (l LogrusAdapter) Errorf(format string, args ...any) { l.Log.Errorf(format, args...) }
func (l LogrusAdapter) Trace(args ...any)                 { l.Log.Trace(args...) }
func (l LogrusAdapter) Debug(args ...any)                 { l.Log.Debug(args...) }
func (l LogrusAdapter) Info(args ...any)                  { l.Log.Info(args...) }
func (l LogrusAdapter) Warn(args ...any)                  { l.Log.Warn(args...) }
func (l LogrusAdapter) Error(args ...any)                 { l.Log.Error(args...) }

func (l LogrusAdapter) WithField(key string, value any) golibrespot.Logger {
	return LogrusAdapter{l.Log.WithField(key, value)}
}

func (l LogrusAdapter) WithError(err error) golibrespot.Logger {
	return LogrusAdapter{l.Log.WithError(err)}
}
