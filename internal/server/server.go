package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/silver/pmvibes/internal/handler"
	"github.com/silver/pmvibes/internal/service"
	"github.com/silver/pmvibes/internal/store"
)

type Config struct {
	Port string
}

type Server struct {
	cfg           Config
	router        *http.ServeMux
	allowedRoutes map[string]struct{}
	simSvc        *service.SimulatorService
	logger        *slog.Logger
	eventLog      store.EventRecorder
}

func New(cfg Config, logger *slog.Logger, eventLog store.EventRecorder) *Server {
	s := &Server{
		cfg:           cfg,
		router:        http.NewServeMux(),
		allowedRoutes: make(map[string]struct{}),
		logger:        logger,
		eventLog:      eventLog,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// Initialize simulator service
	s.simSvc = service.NewSimulatorService(s.logger, s.eventLog)

	financeHandler := handler.NewFinanceHandler(s.simSvc, s.logger)

	// Main finance endpoint - returns full simulation breakdown
	s.register("GET", "/finance", financeHandler.GetStatus)
	s.registerPattern("GET", "/finance/history/{page}", financeHandler.GetHistory)
}

func (s *Server) register(method, path string, h http.HandlerFunc) {
	key := routeKey(method, path)
	s.allowedRoutes[key] = struct{}{}
	s.router.HandleFunc(method+" "+path, h)
}

func (s *Server) registerPattern(method, pattern string, h http.HandlerFunc) {
	s.router.HandleFunc(method+" "+pattern, h)
}

func (s *Server) handler() http.Handler {
	var h http.Handler = s.router
	h = strictAllowlist(s.allowedRoutes, h)
	h = securityHeaders(h)
	return h
}

func (s *Server) Run(ctx context.Context) error {
	// Start the simulator
	if err := s.simSvc.Start(ctx); err != nil {
		s.logger.Error("failed to start simulator", "err", err)
	}

	srv := &http.Server{
		Addr:         ":" + s.cfg.Port,
		Handler:      s.handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		s.simSvc.Stop()
		srv.Shutdown(context.Background())
	}()

	return srv.ListenAndServe()
}
