// https://betterstack.com/community/guides/logging/logging-in-go/#customizing-handlers
package logging

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/fatih/color"
	"github.com/pion/logging"
)

var globalLogLevel slog.Level

type prettyHandlerOptions struct {
	SlogOpts slog.HandlerOptions
}

type prettyHandler struct {
	slog.Handler
	l *log.Logger
}

func (h *prettyHandler) Handle(ctx context.Context, r slog.Record) error {
	level := r.Level.String() + ":"

	switch r.Level {
	case slog.LevelDebug:
		level = color.HiBlackString(level)
	case slog.LevelInfo:
		level = color.BlueString(level)
	case slog.LevelWarn:
		level = color.YellowString(level)
	case slog.LevelError:
		level = color.RedString(level)
	}

	fields := make([]string, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		kv := fmt.Sprintf("%s=%+v", a.Key, a.Value.Any())
		fields = append(fields, strings.TrimSpace(kv))
		return true
	})
	fieldStr := strings.Join(fields, " ")

	timeStr := r.Time.Format("[15:05:05.000]")
	msg := color.CyanString(r.Message)
	if r.Level == slog.LevelError && r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		frame, _ := frames.Next()
		msg = fmt.Sprintf("%s:%d: %s", frame.File, frame.Line, msg)
	}

	h.l.Println(timeStr, level, msg, color.WhiteString(fieldStr))

	return nil
}

func newPrettyHandler(
	out io.Writer,
	opts prettyHandlerOptions,
) *prettyHandler {
	return &prettyHandler{
		Handler: slog.NewTextHandler(out, &opts.SlogOpts),
		l:       log.New(out, "", 0),
	}
}

func SetupDefault(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	globalLogLevel = level

	handler := newPrettyHandler(os.Stdout, prettyHandlerOptions{
		SlogOpts: slog.HandlerOptions{
			AddSource: true,
			Level:     level,
		},
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

// Needed for pion/ice

type leveledLogger struct{}

func leveledLog(level slog.Level, format string, args ...any) {
	if level >= globalLogLevel {
		prefix := fmt.Sprintf("ICE %s: ", level)
		slog.Debug(prefix + fmt.Sprintf(format, args...))
	}
}

func (l leveledLogger) Trace(msg string) {
	l.Tracef(msg)
}

func (l leveledLogger) Tracef(format string, args ...any) {
	leveledLog(slog.LevelDebug-1, format, args...)
}

func (l leveledLogger) Debug(msg string) {
	l.Debugf(msg)
}

func (l leveledLogger) Debugf(format string, args ...any) {
	leveledLog(slog.LevelDebug, format, args...)
}

func (l leveledLogger) Info(msg string) {
	l.Infof(msg)
}

func (l leveledLogger) Infof(format string, args ...any) {
	leveledLog(slog.LevelInfo, format, args...)
}

func (l leveledLogger) Warn(msg string) {
	l.Warnf(msg)
}

func (l leveledLogger) Warnf(format string, args ...any) {
	leveledLog(slog.LevelWarn, format, args...)
}

func (l leveledLogger) Error(msg string) {
	l.Errorf(msg)
}

func (l leveledLogger) Errorf(format string, args ...any) {
	l.Error(fmt.Sprintf("ICE: "+format, args...))
}

type LoggerFactory struct{}

func (f *LoggerFactory) NewLogger(_ string) logging.LeveledLogger {
	return leveledLogger{}
}
