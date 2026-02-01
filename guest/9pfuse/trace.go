package main

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Tracer collects timing information for operations
type Tracer struct {
	enabled atomic.Bool
	mu      sync.Mutex
	ops     map[string]*OpStats
}

// OpStats tracks statistics for an operation type
type OpStats struct {
	Count      int64
	TotalNanos int64
	MinNanos   int64
	MaxNanos   int64
}

var globalTracer = &Tracer{
	ops: make(map[string]*OpStats),
}

// EnableTracing enables/disables tracing
func EnableTracing(enable bool) {
	globalTracer.enabled.Store(enable)
}

// TraceOp records an operation's duration
func TraceOp(op string, start time.Time) {
	if !globalTracer.enabled.Load() {
		return
	}

	duration := time.Since(start).Nanoseconds()

	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()

	stats, ok := globalTracer.ops[op]
	if !ok {
		stats = &OpStats{MinNanos: duration}
		globalTracer.ops[op] = stats
	}

	stats.Count++
	stats.TotalNanos += duration
	if duration < stats.MinNanos {
		stats.MinNanos = duration
	}
	if duration > stats.MaxNanos {
		stats.MaxNanos = duration
	}
}

// DumpStats logs all collected statistics
func DumpStats() {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()

	if len(globalTracer.ops) == 0 {
		log.Println("Trace: no operations recorded")
		return
	}

	// Sort by total time descending
	type opEntry struct {
		name  string
		stats *OpStats
	}
	var entries []opEntry
	for name, stats := range globalTracer.ops {
		entries = append(entries, opEntry{name, stats})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stats.TotalNanos > entries[j].stats.TotalNanos
	})

	log.Println("=== 9pfuse Operation Trace ===")
	log.Printf("%-25s %8s %12s %10s %10s %10s", "Operation", "Count", "Total(ms)", "Avg(ms)", "Min(ms)", "Max(ms)")
	log.Println("----------------------------------------------------------------------------------------------")

	for _, e := range entries {
		s := e.stats
		avgMs := float64(s.TotalNanos) / float64(s.Count) / 1e6
		log.Printf("%-25s %8d %12.2f %10.2f %10.2f %10.2f",
			e.name,
			s.Count,
			float64(s.TotalNanos)/1e6,
			avgMs,
			float64(s.MinNanos)/1e6,
			float64(s.MaxNanos)/1e6,
		)
	}
}

// ResetStats clears all collected statistics
func ResetStats() {
	globalTracer.mu.Lock()
	defer globalTracer.mu.Unlock()
	globalTracer.ops = make(map[string]*OpStats)
}

// SpanTracer is a helper for tracing a span of operations
type SpanTracer struct {
	op    string
	start time.Time
}

// StartSpan starts a new trace span
func StartSpan(op string) *SpanTracer {
	return &SpanTracer{op: op, start: time.Now()}
}

// End ends the span and records its duration
func (s *SpanTracer) End() {
	TraceOp(s.op, s.start)
}

// EndWithDetail ends the span with additional detail
func (s *SpanTracer) EndWithDetail(detail string) {
	TraceOp(fmt.Sprintf("%s(%s)", s.op, detail), s.start)
}
