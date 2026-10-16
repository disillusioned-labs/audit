package app

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/trace"
)

// A valid unsampled parent keeps the instrumented DB ping out of traces
// when the configured sampler is parent-based.
var probeParent = trace.NewSpanContext(trace.SpanContextConfig{
	TraceID: trace.TraceID{0x70, 0x72, 0x6f, 0x62, 0x65, 1},
	SpanID:  trace.SpanID{0x70, 0x72, 0x6f, 0x62, 0x65, 1},
})

type consumerHealth struct {
	maxAge   time.Duration
	lastPoll atomic.Int64
	draining atomic.Bool
	mu       sync.RWMutex
	joined   bool
	assigned map[string]map[int32]struct{}
}

func (h *consumerHealth) progress() {
	h.lastPoll.Store(time.Now().UnixNano())
}

func (h *consumerHealth) onAssigned(_ context.Context, _ *kgo.Client, parts map[string][]int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for topic, partitions := range parts {
		if len(partitions) == 0 {
			continue
		}
		if h.assigned[topic] == nil {
			h.assigned[topic] = make(map[int32]struct{}, len(partitions))
		}
		for _, partition := range partitions {
			h.assigned[topic][partition] = struct{}{}
		}
	}
	h.joined = true
}

func (h *consumerHealth) onRevoked(_ context.Context, _ *kgo.Client, parts map[string][]int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for topic, partitions := range parts {
		for _, partition := range partitions {
			delete(h.assigned[topic], partition)
		}
		if len(h.assigned[topic]) == 0 {
			delete(h.assigned, topic)
		}
	}
	h.joined = false
}

func (h *consumerHealth) ready(member string, generation int32) bool {
	if h.draining.Load() {
		return false
	}
	if member == "" || generation < 0 {
		return false
	}
	h.mu.RLock()
	joined := h.joined
	assigned := len(h.assigned) > 0
	h.mu.RUnlock()
	if !joined {
		return false
	}
	if !assigned {
		return true
	}
	last := h.lastPoll.Load()
	return last != 0 && time.Since(time.Unix(0, last)) <= h.maxAge
}

func (h *consumerHealth) handler(client *kgo.Client, pingDB func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		member, generation := client.GroupMetadata()
		if !h.ready(member, generation) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(trace.ContextWithSpanContext(r.Context(), probeParent), time.Second)
		defer cancel()
		if err := pingDB(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}
