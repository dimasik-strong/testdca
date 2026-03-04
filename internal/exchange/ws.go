package exchange

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

type WSClient struct {
	url       string
	apiKey    string
	apiSecret string
	symbol    string
	logger    *slog.Logger
	conn      *websocket.Conn
	done      chan struct{}
	Events    chan interface{}
}

type ExecutionEvent struct {
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Price    float64 `json:"execPrice,string"`
	Quantity float64 `json:"execQty,string"`
	OrderID  string  `json:"orderId"`
	ExecID   string  `json:"execId"`
}

type OrderEvent struct {
	Symbol   string  `json:"symbol"`
	OrderID  string  `json:"orderId"`
	Status   string  `json:"orderStatus"`
	Price    float64 `json:"price,string"`
	Quantity float64 `json:"qty,string"`
	Side     string  `json:"side"`
}

func NewWSClient(url, apiKey, apiSecret, symbol string, logger *slog.Logger) *WSClient {
	return &WSClient{
		url:       url,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		symbol:    symbol,
		logger:    logger,
		done:      make(chan struct{}),
		Events:    make(chan interface{}, 100),
	}
}

func (c *WSClient) Connect() error {
	var err error
	c.conn, _, err = websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	c.logger.Info("WebSocket connected")

	if c.apiKey != "" && c.apiSecret != "" {
		expires := time.Now().UnixMilli() + 30000
		expiresStr := strconv.FormatInt(expires, 10)

		signPayload := "GET/realtime" + expiresStr
		c.logger.Debug("Sign payload", "payload", signPayload)

		h := hmac.New(sha256.New, []byte(c.apiSecret))
		h.Write([]byte(signPayload))
		signature := hex.EncodeToString(h.Sum(nil))

		authMsg := map[string]interface{}{
			"op":   "auth",
			"args": []interface{}{c.apiKey, expires, signature},
		}
		c.logger.Debug("Sending auth", "msg", authMsg)

		if err := c.conn.WriteJSON(authMsg); err != nil {
			return fmt.Errorf("auth message: %w", err)
		}
		c.logger.Info("Authentication sent")
	}

	subMsg := map[string]interface{}{
		"op": "subscribe",
		"args": []string{
			"execution",
			"order",
		},
	}
	c.logger.Debug("Sending subscribe", "msg", subMsg)
	if err := c.conn.WriteJSON(subMsg); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	c.logger.Info("Subscribed to private channels")

	go c.readPump()

	go c.pingPump()

	return nil
}

func (c *WSClient) readPump() {
	defer close(c.done)
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			c.logger.Error("WebSocket read error", "error", err)
			return
		}

		var raw map[string]interface{}
		if err := json.Unmarshal(message, &raw); err != nil {
			c.logger.Error("Failed to parse message", "error", err)
			continue
		}

		c.logger.Debug("WS message", "msg", raw)

		if op, ok := raw["op"].(string); ok {
			switch op {
			case "auth":
				if success, ok := raw["success"].(bool); ok && success {
					c.logger.Info("Authentication successful")
				} else {
					c.logger.Error("Authentication failed", "msg", raw)
				}
			case "subscribe":
				if success, ok := raw["success"].(bool); ok && success {
					c.logger.Info("Subscription successful")
				} else {
					c.logger.Error("Subscription failed", "msg", raw)
				}
			}
			continue
		}

		if dataVal, ok := raw["data"]; ok {
			switch v := dataVal.(type) {
			case []interface{}:
				for _, item := range v {
					if itemMap, ok := item.(map[string]interface{}); ok {
						c.processData(itemMap)
					}
				}
			case map[string]interface{}:
				c.processData(v)
			}
		}
	}
}

func (c *WSClient) processData(data map[string]interface{}) {
	if _, hasExec := data["execId"]; hasExec {
		var ev ExecutionEvent
		b, _ := json.Marshal(data)
		if err := json.Unmarshal(b, &ev); err != nil {
			c.logger.Error("failed to parse execution event", "error", err)
		} else {
			c.Events <- ev
		}
	} else if _, hasOrder := data["orderId"]; hasOrder {
		var ev OrderEvent
		b, _ := json.Marshal(data)
		if err := json.Unmarshal(b, &ev); err != nil {
			c.logger.Error("failed to parse order event", "error", err)
		} else {
			c.Events <- ev
		}
	}
}

func (c *WSClient) pingPump() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				c.logger.Error("ping error", "error", err)
				return
			}
		case <-c.done:
			return
		}
	}
}

// Close закрывает соединение.
func (c *WSClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
