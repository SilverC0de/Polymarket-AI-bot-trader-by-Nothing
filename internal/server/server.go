package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/silver/pmvibes/internal/handler"
	"github.com/silver/pmvibes/internal/service"
	"github.com/silver/pmvibes/internal/store"
	"github.com/silver/pmvibes/pkg/polymarket"
)

type Config struct {
	Port string
}

type Server struct {
	cfg      Config
	router   *http.ServeMux
	simSvc   *service.SimulatorService
	logger   *slog.Logger
	eventLog *store.EventLog
}

func New(cfg Config, logger *slog.Logger, eventLog *store.EventLog) *Server {
	s := &Server{
		cfg:      cfg,
		router:   http.NewServeMux(),
		logger:   logger,
		eventLog: eventLog,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	pmClient := polymarket.NewClient()
	financeSvc := service.NewFinanceService(pmClient)

	// Initialize simulator service
	s.simSvc = service.NewSimulatorService(s.logger, s.eventLog)

	healthHandler := handler.NewHealthHandler()
	financeHandler := handler.NewFinanceHandler(financeSvc, s.simSvc)

	s.router.HandleFunc("GET /health", healthHandler.Health)

	// Main finance endpoint - returns full simulation breakdown
	s.router.HandleFunc("GET /finance", financeHandler.GetStatus)
	s.router.HandleFunc("GET /finance/history", financeHandler.GetHistory)

	s.router.HandleFunc("GET /finance/positions", financeHandler.GetPositions)
	s.router.HandleFunc("GET /finance/predictions", financeHandler.GetPredictions)
	s.router.HandleFunc("POST /finance/trade", financeHandler.ExecuteTrade)
}

func (s *Server) Run(ctx context.Context) error {
	// Start the simulator
	if err := s.simSvc.Start(ctx); err != nil {
		s.logger.Error("failed to start simulator", "err", err)
	}

	srv := &http.Server{
		Addr:         ":" + s.cfg.Port,
		Handler:      s.router,
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
