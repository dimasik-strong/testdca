package main

import (
	"dca-bot/internal/config"
	"dca-bot/internal/engine"
	"dca-bot/internal/exchange"
	"dca-bot/internal/logger"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Runtime.LogLevel)

	ex := exchange.NewBybitClient(cfg.Exchange.BaseURL, cfg.Exchange.APIKey, cfg.Exchange.Secret)

	info, err := ex.GetSymbolInfo(cfg.Bot.Symbol)
	if err != nil {
		log.Error("failed to get symbol info", "error", err)
		os.Exit(1)
	}
	log.Info("symbol info", "tickSize", info.TickSize, "lotSize", info.LotSize)

	ws := exchange.NewWSClient(cfg.Exchange.WSURL, cfg.Exchange.APIKey, cfg.Exchange.Secret, cfg.Bot.Symbol, log)
	if err := ws.Connect(); err != nil {
		log.Error("failed to connect WebSocket", "error", err)
		os.Exit(1)
	}
	defer ws.Close()

	engCfg := &engine.Config{
		TpPercent:        cfg.Bot.TpPercent,
		SOCount:          cfg.Bot.SOCount,
		SOStepPercent:    cfg.Bot.SOStepPercent,
		SOStepMultiplier: cfg.Bot.SOStepMultiplier,
		SOBaseQty:        cfg.Bot.SOBaseQty,
		SOQtyMultiplier:  cfg.Bot.SOQtyMultiplier,
	}

	eng := engine.NewEngine(ex, cfg.Bot.Symbol, cfg.Bot.Side, cfg.Bot.BaseOrderQty, engCfg, info.TickSize, info.LotSize, log)

	if err := eng.Start(); err != nil {
		log.Error("engine start failed", "error", err)
		os.Exit(1)
	}

	log.Info("engine started, waiting for events")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

loop:
	for {
		select {
		case ev := <-ws.Events:
			switch e := ev.(type) {
			case exchange.ExecutionEvent:
				log.Info("execution event received in main", "orderID", e.OrderID, "price", e.Price, "qty", e.Quantity)
				eng.OnExecution(e)
			case exchange.OrderEvent:
				log.Info("order event", "symbol", e.Symbol, "orderID", e.OrderID, "status", e.Status)
			}
		case <-sigCh:
			log.Info("shutting down, cancelling all orders...")
			eng.CancelAllOrders()
			break loop
		}
	}

	log.Info("bye")
}
