package librespot

import (
	"fmt"
	"regexp"
	"strings"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/sirupsen/logrus"
)

type LogrusAdapter struct {
	Log            *logrus.Entry
	OnAuthRequired func(string)
}

var authURLPattern = regexp.MustCompile(`https?://[^\s]+`)

func authURLFromMessage(format string, args ...any) (string, bool) {
	msg := fmt.Sprintf(format, args...)
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "visit the following link") || strings.Contains(lower, "complete authentication") {
		link := authURLPattern.FindString(msg)
		link = strings.TrimRight(link, ".,;)]}")
		return link, true
	}
	return "", false
}

func (l LogrusAdapter) Tracef(format string, args ...any) { l.Log.Tracef(format, args...) }
func (l LogrusAdapter) Debugf(format string, args ...any) { l.Log.Debugf(format, args...) }
func (l LogrusAdapter) Infof(format string, args ...any) {
	if link, required := authURLFromMessage(format, args...); required {
		if l.OnAuthRequired != nil {
			l.OnAuthRequired(link)
		}
		// Do not leak the one-time authorization URL into the log file.
		l.Log.Info("Spotify authentication is required")
		return
	}
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
	return LogrusAdapter{Log: l.Log.WithField(key, value), OnAuthRequired: l.OnAuthRequired}
}

func (l LogrusAdapter) WithError(err error) golibrespot.Logger {
	return LogrusAdapter{Log: l.Log.WithError(err), OnAuthRequired: l.OnAuthRequired}
}
