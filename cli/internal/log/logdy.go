package log

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/logdyhq/logdy-core/logdy"
)

type LogdyHandler struct {
	inner   slog.Handler
	logdy   logdy.Logdy
	once    sync.Once
	enabled bool
	mux     *http.ServeMux
}

var cfg = logdy.Config{
	HttpPathPrefix: "/logdy",
}

func NewLogdyHandler(inner slog.Handler, enabled bool, mux *http.ServeMux) *LogdyHandler {
	return &LogdyHandler{
		inner:   inner,
		enabled: enabled,
		mux:     mux,
	}
}

func (h *LogdyHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *LogdyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogdyHandler{
		inner:   h.inner.WithAttrs(attrs),
		enabled: h.enabled,
		mux:     h.mux,
		logdy:   h.logdy,
	}
}

func (h *LogdyHandler) WithGroup(name string) slog.Handler {
	return &LogdyHandler{
		inner:   h.inner.WithGroup(name),
		enabled: h.enabled,
		mux:     h.mux,
		logdy:   h.logdy,
	}
}

func (h *LogdyHandler) Handle(ctx context.Context, rec slog.Record) error {
	if err := h.inner.Handle(ctx, rec); err != nil {
		slog.Error("error writing slog inner handler", "error", err)
	}

	if !h.enabled {
		return nil
	}

	// Lazy init logdy with the existing mux
	h.once.Do(func() {
		ld := logdy.InitializeLogdy(cfg, h.mux)
		if ld == nil {
			slog.Error("Logdy init failed")
		}
		h.logdy = ld
	})

	if h.logdy == nil {
		return nil
	}

	fields := make(logdy.Fields)
	rec.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = a.Value.Any()
		return true
	})

	fields["level"] = rec.Level.String()
	fields["time"] = rec.Time.Format(time.RFC3339Nano)
	fields["msg"] = rec.Message

	h.logdy.Log(fields)

	return nil
}
