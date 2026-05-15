package http

import (
	"encoding/json"
	"net/http"
	"time"
)

type Server struct {
	httpPort int
	cdpPort   int
	statusFn  func() map[string]interface{}
}

func NewServer(httpPort, cdpPort int, statusFn func() map[string]interface{}) *Server {
	return &Server{
		httpPort: httpPort,
		cdpPort:  cdpPort,
		statusFn: statusFn,
	}
}

func (s *Server) Serve() *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		status := s.statusFn()
		uptime := 0
		if t, ok := status["startTime"].(time.Time); ok {
			uptime = int(time.Since(t).Seconds())
		}
		out := map[string]interface{}{
			"status": "ok",
			"ports":  map[string]int{"cdp": s.cdpPort, "http": s.httpPort},
			"uptime": uptime,
		}
		if chrome, ok := status["cdp"].(map[string]interface{}); ok {
			out["chrome"] = chrome
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.statusFn())
	})

	return &http.Server{Addr: "", Handler: mux}
}