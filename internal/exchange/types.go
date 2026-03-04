package exchange

type SymbolInfo struct {
	TickSize float64
	LotSize  float64
	MinQty   float64
}

type Order struct {
	ID            string
	ClientOrderID string
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
