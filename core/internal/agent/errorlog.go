// errorlog.go routes this turn's errors into the session's error.log.
// Every session folder (and every subagent session, which is a session
// with its own folder) keeps one, so a failure can be understood later
// from the raw error instead of a summary. Nothing here changes what a
// turn returns: logging is best-effort (see internal/errlog).

package agent

import "github.com/Ady-JK/paladin/core/internal/errlog"

// logError appends one error to this session's error.log.
func (a *Agent) logError(source string, err error) {
	if a.session.Dir == "" {
		return
	}
	errlog.Append(a.session.Dir, source, err)
}

// logErrorf appends a formatted entry to this session's error.log.
func (a *Agent) logErrorf(source, format string, args ...any) {
	if a.session.Dir == "" {
		return
	}
	errlog.Appendf(a.session.Dir, source, format, args...)
}
