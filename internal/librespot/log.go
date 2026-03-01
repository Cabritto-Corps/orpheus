package librespot

import (
	"fmt"
	"os"
	"strings"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/sirupsen/logrus"
)

type LogrusAdapter struct {
	Log *logrus.Entry
}

// authKeywords are substrings that indicate the user must take action.
// When matched, the message is echoed to stderr so it's visible even though
// the main log goes to a file.
var authKeywords = []string{
	"complete authentication",
	"visit the following link",
}

func stderrIfAuth(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	for _, kw := range authKeywords {
		if strings.Contains(strings.ToLower(msg), kw) {
			fmt.Fprintln(os.Stderr, "\n[orpheus] "+msg)
			return
		}
	}
}

func (l LogrusAdapter) Tracef(format string, args ...interface{}) { l.Log.Tracef(format, args...) }
func (l LogrusAdapter) Debugf(format string, args ...interface{}) { l.Log.Debugf(format, args...) }
func (l LogrusAdapter) Infof(format string, args ...interface{}) {
	stderrIfAuth(format, args...)
	l.Log.Infof(format, args...)
}
func (l LogrusAdapter) Warnf(format string, args ...interface{})  { l.Log.Warnf(format, args...) }
func (l LogrusAdapter) Errorf(format string, args ...interface{}) { l.Log.Errorf(format, args...) }
func (l LogrusAdapter) Trace(args ...interface{})                 { l.Log.Trace(args...) }
func (l LogrusAdapter) Debug(args ...interface{})                 { l.Log.Debug(args...) }
func (l LogrusAdapter) Info(args ...interface{})                  { l.Log.Info(args...) }
func (l LogrusAdapter) Warn(args ...interface{})                  { l.Log.Warn(args...) }
func (l LogrusAdapter) Error(args ...interface{})                 { l.Log.Error(args...) }

func (l LogrusAdapter) WithField(key string, value interface{}) golibrespot.Logger {
	return LogrusAdapter{l.Log.WithField(key, value)}
}

func (l LogrusAdapter) WithError(err error) golibrespot.Logger {
	return LogrusAdapter{l.Log.WithError(err)}
}
