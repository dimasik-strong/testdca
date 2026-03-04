package exchange

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type BybitClient struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewBybitClient(baseURL, apiKey, apiSecret string) *BybitClient {
	return &BybitClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *BybitClient) sign(payload string) string {
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

func (c *BybitClient) request(method, path string, params map[string]string) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// query string для подписи
	query := ""
	for i, k := range keys {
		if i > 0 {
			query += "&"
		}
		query += k + "=" + params[k]
	}

	// Подпись
	signPayload := timestamp + c.apiKey + recvWindow + query
	signature := c.sign(signPayload)

	var req *http.Request
	var err error

	switch method {
	case "GET":
		fullURL := c.baseURL + path
		if query != "" {
			fullURL += "?" + query
		}
		req, err = http.NewRequest(method, fullURL, nil)
		if err != nil {
			return nil, err
		}

	case "POST":
		formData := url.Values{}
		for k, v := range params {
			formData.Set(k, v)
		}
		req, err = http.NewRequest(method, c.baseURL+path, strings.NewReader(formData.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}

	req.Header.Set("X-BAPI-API-KEY", c.apiKey)
	req.Header.Set("X-BAPI-TIMESTAMP", timestamp)
	req.Header.Set("X-BAPI-SIGN", signature)
	req.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

func (c *BybitClient) PlaceOrder(symbol, side, orderType string, quantity, price float64, clientOrderID string) (*Order, error) {
	params := map[string]string{
		"symbol":      symbol,
		"side":        side,
		"orderType":   orderType,
		"timeInForce": "GTC",
		"orderLinkId": clientOrderID,
		"category":    "spot",
	}

	if orderType == "LIMIT" {
		params["price"] = strconv.FormatFloat(price, 'f', 8, 64)
		params["qty"] = strconv.FormatFloat(quantity, 'f', 2, 64)
	} else {
		params["marketUnit"] = "quoteCoin"
		params["qty"] = strconv.FormatFloat(quantity, 'f', 2, 64)
	}

	body, err := c.request("POST", "/v5/order/create", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int `json:"retCode"`
		Result  struct {
			OrderId     string `json:"orderId"`
			OrderLinkId string `json:"orderLinkId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit error %d: %s", resp.RetCode, body)
	}

	return &Order{
		ID:            resp.Result.OrderId,
		ClientOrderID: resp.Result.OrderLinkId,
		Symbol:        symbol,
		Side:          side,
		Type:          orderType,
		Quantity:      quantity,
		Price:         price,
	}, nil
}

func (c *BybitClient) CancelOrder(symbol, orderID string) error {
	params := map[string]string{
		"symbol":   symbol,
		"orderId":  orderID,
		"category": "spot",
	}
	_, err := c.request("POST", "/v5/order/cancel", params)
	return err
}

func (c *BybitClient) GetSymbolInfo(symbol string) (*SymbolInfo, error) {
	params := map[string]string{
		"symbol":   symbol,
		"category": "spot",
	}
	body, err := c.request("GET", "/v5/market/instruments-info", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		RetCode int `json:"retCode"`
		Result  struct {
			List []struct {
				Symbol      string `json:"symbol"`
				PriceFilter struct {
					TickSize string `json:"tickSize"`
				} `json:"priceFilter"`
				LotSizeFilter struct {
					MaxOrderQty string `json:"maxOrderQty"`
					MinOrderQty string `json:"minOrderQty"`
					QtyStep     string `json:"qtyStep"`
				} `json:"lotSizeFilter"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Result.List) == 0 {
		return nil, fmt.Errorf("symbol not found")
	}
	info := resp.Result.List[0]
	tickSize, _ := strconv.ParseFloat(info.PriceFilter.TickSize, 64)
	lotSize, _ := strconv.ParseFloat(info.LotSizeFilter.QtyStep, 64)
	minQty, _ := strconv.ParseFloat(info.LotSizeFilter.MinOrderQty, 64)
	return &SymbolInfo{
		TickSize: tickSize,
		LotSize:  lotSize,
		MinQty:   minQty,
	}, nil
}
