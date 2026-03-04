package engine

import (
	"dca-bot/internal/exchange"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

type Config struct {
	TpPercent        float64
	SOCount          int
	SOStepPercent    float64
	SOStepMultiplier float64
	SOBaseQty        float64
	SOQtyMultiplier  float64
}

type SafetyOrder struct {
	Price float64
	Qty   float64
}

type Engine struct {
	ex       *exchange.BybitClient
	symbol   string
	side     string
	baseQty  float64
	cfg      *Config
	logger   *slog.Logger
	tickSize float64
	lotSize  float64
	mu       sync.RWMutex

	entryPrice     float64
	totalQty       float64
	marketOrderID  string
	tpOrderID      string
	soOrderIDs     []string
	active         bool
	entryProcessed bool
}

func NewEngine(ex *exchange.BybitClient, symbol, side string, baseQty float64, cfg *Config, tickSize, lotSize float64, logger *slog.Logger) *Engine {
	return &Engine{
		ex:         ex,
		symbol:     symbol,
		side:       side,
		baseQty:    baseQty,
		cfg:        cfg,
		logger:     logger,
		tickSize:   tickSize,
		lotSize:    lotSize,
		soOrderIDs: []string{},
		active:     false,
	}
}

func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	clientOrderID := fmt.Sprintf("entry-%d", time.Now().UnixNano())
	order, err := e.ex.PlaceOrder(e.symbol, e.side, "MARKET", e.baseQty, 0, clientOrderID)
	if err != nil {
		return fmt.Errorf("market order failed: %w", err)
	}
	e.marketOrderID = order.ID
	e.active = true
	e.entryProcessed = false
	e.logger.Info("market order placed", "orderID", order.ID)
	return nil
}

func (e *Engine) OnExecution(exec exchange.ExecutionEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Debug("OnExecution called", "execOrderID", exec.OrderID, "marketOrderID", e.marketOrderID, "tpOrderID", e.tpOrderID, "active", e.active)

	if !e.active {
		e.logger.Debug("engine not active, ignoring execution")
		return
	}

	// Исполнение маркет-ордера
	if exec.OrderID == e.marketOrderID {
		// Если это первое исполнение
		if !e.entryProcessed {
			e.entryPrice = exec.Price
			e.totalQty = exec.Quantity
			e.entryProcessed = true
			e.logger.Info("market order fill, placing TP and safety orders", "price", exec.Price, "qty", exec.Quantity)
			e.placeTP()
			e.placeSafetyOrders()
		} else {
			// Дополнительные частичные исполнения (редко)
			newTotal := e.totalQty + exec.Quantity
			if newTotal > 0 {
				e.entryPrice = (e.entryPrice*e.totalQty + exec.Price*exec.Quantity) / newTotal
				e.totalQty = newTotal
				e.logger.Info("additional market order fill, updated avg", "avg", e.entryPrice, "totalQty", e.totalQty)
			}
		}
		return
	}

	// Исполнение TP
	if exec.OrderID == e.tpOrderID && e.tpOrderID != "" {
		e.logger.Info("TP filled, trade closed")
		e.active = false
		e.cancelAllSafetyOrders()
		return
	}

	// Исполнение страховочного ордера
	for _, soID := range e.soOrderIDs {
		if exec.OrderID == soID {
			e.logger.Info("safety order filled", "price", exec.Price, "qty", exec.Quantity)
			newTotal := e.totalQty + exec.Quantity
			if newTotal > 0 {
				e.entryPrice = (e.entryPrice*e.totalQty + exec.Price*exec.Quantity) / newTotal
				e.totalQty = newTotal
				e.logger.Info("new avg price", "avg", e.entryPrice, "totalQty", e.totalQty)
			}
			e.placeTP()
			e.removeSO(exec.OrderID)
			return
		}
	}
	e.logger.Debug("execution event ignored", "orderID", exec.OrderID)
}

func (e *Engine) placeTP() {
	if e.totalQty == 0 {
		e.logger.Warn("placeTP: totalQty is 0, skipping")
		return
	}
	if e.entryPrice == 0 {
		e.logger.Warn("placeTP: entryPrice is 0, skipping")
		return
	}

	if e.tpOrderID != "" {
		e.logger.Info("cancelling old TP", "orderID", e.tpOrderID)
		if err := e.ex.CancelOrder(e.symbol, e.tpOrderID); err != nil {
			e.logger.Error("failed to cancel old TP", "error", err)
		}
		e.tpOrderID = ""
	}

	var tpPrice float64
	if e.side == "BUY" {
		tpPrice = e.entryPrice * (1 + e.cfg.TpPercent/100)
	} else {
		tpPrice = e.entryPrice * (1 - e.cfg.TpPercent/100)
	}
	tpPrice = roundPrice(tpPrice, e.tickSize)
	e.logger.Debug("calculated TP price", "entryPrice", e.entryPrice, "tpPercent", e.cfg.TpPercent, "tpPrice", tpPrice)

	qty := roundQty(e.totalQty, e.lotSize)
	e.logger.Debug("rounded qty for TP", "original", e.totalQty, "rounded", qty, "lotSize", e.lotSize)

	clientOrderID := fmt.Sprintf("tp-%d", time.Now().UnixNano())
	order, err := e.ex.PlaceOrder(e.symbol, oppositeSide(e.side), "LIMIT", qty, tpPrice, clientOrderID)
	if err != nil {
		e.logger.Error("failed to place TP", "error", err)
		return
	}
	e.tpOrderID = order.ID
	e.logger.Info("TP placed successfully", "price", tpPrice, "qty", qty, "orderID", order.ID)
}

func (e *Engine) calculateSafetyOrders() ([]SafetyOrder, error) {
	if e.entryPrice == 0 {
		return nil, fmt.Errorf("entry price not set")
	}
	if e.cfg.SOCount <= 0 {
		return nil, nil
	}

	orders := make([]SafetyOrder, e.cfg.SOCount)
	for i := 0; i < e.cfg.SOCount; i++ {
		stepPercent := e.cfg.SOStepPercent * math.Pow(e.cfg.SOStepMultiplier, float64(i))

		var price float64
		if e.side == "BUY" {
			price = e.entryPrice * (1 - stepPercent/100)
		} else {
			price = e.entryPrice * (1 + stepPercent/100)
		}
		price = roundPrice(price, e.tickSize)

		qty := e.cfg.SOBaseQty * math.Pow(e.cfg.SOQtyMultiplier, float64(i))
		qty = roundQty(qty, e.lotSize)

		orders[i] = SafetyOrder{Price: price, Qty: qty}
		e.logger.Debug("calculated safety order", "index", i, "price", price, "qty", qty)
	}
	return orders, nil
}

func (e *Engine) placeSafetyOrders() {
	if len(e.soOrderIDs) > 0 {
		e.logger.Debug("safety orders already placed, skipping")
		return
	}
	orders, err := e.calculateSafetyOrders()
	if err != nil {
		e.logger.Error("failed to calculate safety orders", "error", err)
		return
	}

	for _, so := range orders {
		clientOrderID := fmt.Sprintf("so-%d", time.Now().UnixNano())
		order, err := e.ex.PlaceOrder(e.symbol, e.side, "LIMIT", so.Qty, so.Price, clientOrderID)
		if err != nil {
			e.logger.Error("failed to place safety order", "price", so.Price, "qty", so.Qty, "error", err)
			continue
		}
		e.soOrderIDs = append(e.soOrderIDs, order.ID)
		e.logger.Info("safety order placed", "price", so.Price, "qty", so.Qty, "orderID", order.ID)
	}
}

func (e *Engine) cancelAllSafetyOrders() {
	for _, id := range e.soOrderIDs {
		if err := e.ex.CancelOrder(e.symbol, id); err != nil {
			e.logger.Error("failed to cancel SO", "orderID", id, "error", err)
		}
	}
	e.soOrderIDs = []string{}
}

func (e *Engine) removeSO(id string) {
	for i, soID := range e.soOrderIDs {
		if soID == id {
			e.soOrderIDs = append(e.soOrderIDs[:i], e.soOrderIDs[i+1:]...)
			break
		}
	}
}

func (e *Engine) CancelAllOrders() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.active {
		return
	}
	if e.tpOrderID != "" {
		e.logger.Info("cancelling TP on shutdown", "orderID", e.tpOrderID)
		if err := e.ex.CancelOrder(e.symbol, e.tpOrderID); err != nil {
			e.logger.Error("failed to cancel TP on shutdown", "error", err)
		}
		e.tpOrderID = ""
	}
	for _, id := range e.soOrderIDs {
		e.logger.Info("cancelling SO on shutdown", "orderID", id)
		if err := e.ex.CancelOrder(e.symbol, id); err != nil {
			e.logger.Error("failed to cancel SO on shutdown", "orderID", id, "error", err)
		}
	}
	e.soOrderIDs = []string{}
	e.logger.Info("all orders cancelled on shutdown")
}

func roundPrice(price, tickSize float64) float64 {
	if tickSize <= 0 {
		return price
	}
	return math.Round(price/tickSize) * tickSize
}

func roundQty(qty, lotSize float64) float64 {
	if lotSize > 0 {
		return math.Round(qty/lotSize) * lotSize
	}
	return math.Round(qty*100) / 100
}

func oppositeSide(side string) string {
	if side == "BUY" {
		return "SELL"
	}
	return "BUY"
}
