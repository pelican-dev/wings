package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Minenetpro/pelican-wings/environment"
	"github.com/Minenetpro/pelican-wings/events"
	"github.com/Minenetpro/pelican-wings/router/middleware"
	"github.com/Minenetpro/pelican-wings/server"
	"github.com/Minenetpro/pelican-wings/system"
)

// SSE event payload types — all include server_id for client-side demuxing.

type sseConsoleData struct {
	ServerID string `json:"server_id"`
	Line     string `json:"line"`
}

type sseStatusData struct {
	ServerID string `json:"server_id"`
	State    string `json:"state"`
}

type sseStatsData struct {
	ServerID         string                `json:"server_id"`
	MemoryBytes      uint64                `json:"memory_bytes"`
	MemoryLimitBytes uint64                `json:"memory_limit_bytes"`
	CpuAbsolute      float64               `json:"cpu_absolute"`
	Network          environment.NetworkStats `json:"network"`
	Uptime           int64                 `json:"uptime"`
	State            string                `json:"state"`
	DiskBytes        int64                 `json:"disk_bytes"`
}

// ssePayload is the internal fan-in type sent from per-server goroutines to the
// main SSE write loop.
type ssePayload struct {
	event string      // SSE event name: "console output", "status", "stats"
	data  interface{} // One of sseConsoleData, sseStatusData, sseStatsData
}

// consoleHistoryResponse is the JSON response for GET /api/servers/:server/console.
type consoleHistoryResponse struct {
	State     string   `json:"state"`
	LineCount int      `json:"line_count"`
	Lines     []string `json:"lines"`
}

// procToSSEStats extracts stats from a server, building an sseStatsData
// without copying the ResourceUsage mutex.
func procToSSEStats(s *server.Server) sseStatsData {
	//nolint:govet // Proc() returns a copy containing a mutex; read-only use is safe here.
	ru := s.Proc()
	return sseStatsData{
		ServerID:         s.ID(),
		MemoryBytes:      ru.Memory,
		MemoryLimitBytes: ru.MemoryLimit,
		CpuAbsolute:      ru.CpuAbsolute,
		Network:          ru.Network,
		Uptime:           ru.Uptime,
		State:            ru.State.Load(),
		DiskBytes:        ru.Disk,
	}
}

// writeSSE writes a single SSE event to the response writer. Returns false if
// the write fails (client disconnected).
func writeSSE(w gin.ResponseWriter, event string, data interface{}) bool {
	b, err := json.Marshal(data)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if err != nil {
		return false
	}
	w.Flush()
	return true
}

// getServerEvents streams SSE events for one or more servers.
//
// Route: GET /api/events?servers=uuid1,uuid2,...
func getServerEvents(c *gin.Context) {
	manager := middleware.ExtractManager(c)

	raw := c.Query("servers")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The servers query parameter is required."})
		return
	}

	ids := strings.Split(raw, ",")
	servers := make([]*server.Server, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		s, ok := manager.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Server %s was not found.", id)})
			return
		}
		servers = append(servers, s)
	}

	if len(servers) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No valid server UUIDs provided."})
		return
	}

	// Set SSE headers.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	ctx, cancel := context.WithCancel(c.Request.Context())

	outChan := make(chan ssePayload, len(servers)*8)

	// Track all subscribed channels for cleanup.
	type subscription struct {
		s       *server.Server
		eventCh chan []byte
		logCh   chan []byte
	}
	subs := make([]subscription, 0, len(servers))

	for _, s := range servers {
		eventCh := make(chan []byte, 8)
		logCh := make(chan []byte, 8)

		s.Events().On(eventCh)
		s.Sink(system.LogSink).On(logCh)

		subs = append(subs, subscription{s: s, eventCh: eventCh, logCh: logCh})

		go func(s *server.Server, eventCh, logCh chan []byte) {
			sid := s.ID()
			for {
				select {
				case <-ctx.Done():
					return
				case <-s.Context().Done():
					// Server deleted mid-stream.
					select {
					case outChan <- ssePayload{event: "status", data: sseStatusData{ServerID: sid, State: "deleted"}}:
					case <-ctx.Done():
					}
					return
				case b, ok := <-logCh:
					if !ok {
						return
					}
					select {
					case outChan <- ssePayload{event: "console output", data: sseConsoleData{ServerID: sid, Line: string(b)}}:
					case <-ctx.Done():
						return
					}
				case b, ok := <-eventCh:
					if !ok {
						return
					}
					var e events.Event
					if err := events.DecodeTo(b, &e); err != nil {
						continue
					}
					switch e.Topic {
					case server.StatusEvent:
						state, _ := e.Data.(string)
						select {
						case outChan <- ssePayload{event: "status", data: sseStatusData{ServerID: sid, State: state}}:
						case <-ctx.Done():
							return
						}
					case server.StatsEvent:
						// Marshal then unmarshal to get plain types (avoids AtomicString).
						raw, err := json.Marshal(e.Data)
						if err != nil {
							continue
						}
						var stats sseStatsData
						if err := json.Unmarshal(raw, &stats); err != nil {
							continue
						}
						stats.ServerID = sid
						select {
						case outChan <- ssePayload{event: "stats", data: stats}:
						case <-ctx.Done():
							return
						}
					case server.ConsoleOutputEvent:
						line, _ := e.Data.(string)
						select {
						case outChan <- ssePayload{event: "console output", data: sseConsoleData{ServerID: sid, Line: line}}:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}(s, eventCh, logCh)
	}

	// Deferred cleanup: cancel context first to signal goroutines, then
	// unsubscribe all channels. This ordering ensures goroutines exit via
	// ctx.Done() before Off() closes their channels.
	defer func() {
		cancel()
		for _, sub := range subs {
			sub.s.Events().Off(sub.eventCh)
			sub.s.Sink(system.LogSink).Off(sub.logCh)
		}
	}()

	// Send initial status and stats for each server.
	for _, s := range servers {
		stats := procToSSEStats(s)
		if !writeSSE(c.Writer, "status", sseStatusData{ServerID: s.ID(), State: stats.State}) {
			return
		}
		if !writeSSE(c.Writer, "stats", stats) {
			return
		}
	}

	// Main event loop.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case p := <-outChan:
			if !writeSSE(c.Writer, p.event, p.data) {
				return
			}
		case <-ticker.C:
			// Keepalive comment.
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// getServerConsole returns recent console log history for a single server.
//
// Route: GET /api/servers/:server/console
func getServerConsole(c *gin.Context) {
	s := middleware.ExtractServer(c)

	l, _ := strconv.Atoi(c.DefaultQuery("size", "100"))
	if l <= 0 {
		l = 100
	} else if l > 100 {
		l = 100
	}

	out, err := s.ReadLogfile(l)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	c.JSON(http.StatusOK, consoleHistoryResponse{
		State:     s.Environment.State(),
		LineCount: len(out),
		Lines:     out,
	})
}
