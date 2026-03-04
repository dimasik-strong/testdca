package exchange

type SymbolInfo struct {
	TickSize float64 // минимальный шаг цены
	LotSize  float64 // минимальный шаг количества
	MinQty   float64 // минимальный объём
}

type Order struct {
	ID            string
	ClientOrderID string // orderLinkId для идемпотентности
	Symbol        string
	Side          string
	Type          string
	Price         float64
	Quantity      float64
	ExecutedQty   float64
	Status        string
	AvgPrice      float64
}

type Execution struct {
	Symbol   string
	Side     string
	Price    float64
	Quantity float64
	OrderID  string
}
