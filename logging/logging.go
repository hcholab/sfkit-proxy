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
		level = color.MagentaString(level)
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

func (l leveledLogger) Trace(msg string) {
	slog.Debug("ICE TRACE:     " + msg)
}

func (l leveledLogger) Tracef(format string, args ...any) {
	l.Trace(fmt.Sprintf(format, args...))
}

func (l leveledLogger) Debug(msg string) {
	slog.Debug(msg)
}

func (l leveledLogger) Debugf(format string, args ...any) {
	slog.Debug(fmt.Sprintf(format, args...))
}

func (l leveledLogger) Info(msg string) {
	slog.Info(msg)
}

func (l leveledLogger) Infof(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

func (l leveledLogger) Warn(msg string) {
	slog.Debug(msg)
}

func (l leveledLogger) Warnf(format string, args ...any) {
	slog.Debug(fmt.Sprintf(format, args...))
}

func (l leveledLogger) Error(msg string) {
	slog.Debug(msg)
}

func (l leveledLogger) Errorf(format string, args ...any) {
	slog.Debug(fmt.Sprintf(format, args...))
}

type LoggerFactory struct{}

func (f *LoggerFactory) NewLogger(_ string) logging.LeveledLogger {
	return leveledLogger{}
}
