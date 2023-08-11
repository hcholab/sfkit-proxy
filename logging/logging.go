// https://betterstack.com/community/guides/logging/logging-in-go/#customizing-handlers
package logging

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/fatih/color"
	"golang.org/x/exp/slog"
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
			Level: level,
		},
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)
}
