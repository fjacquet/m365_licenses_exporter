package app

import (
	"net/http"
	"sync/atomic"
)

// Health reports 503 "starting" until the first collection cycle completes,
// then 200 "ok" (design spec §2).
type Health struct {
	ready atomic.Bool
}

func (h *Health) SetReady() { h.ready.Store(true) }

func (h *Health) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if h.ready.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("starting"))
}
