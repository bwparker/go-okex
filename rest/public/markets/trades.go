package markets

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bwparker/go-okex/types"
	"github.com/google/go-querystring/query"
)

// query
// ?limit={limit}&start_time={start_time}&end_time={end_time}
type RequestForTrades struct {
	ProductCode string `url:"-"`
	Limit       int    `url:"limit,omitempty"`
	Start       int64  `url:"start,omitempty"`
	End         int64  `url:"end,omitempty"`
}

type ResponseForTrades []Trade

type Trade struct {
	InstrumentID string              `json:"instrument_id"`
	Price        types.StringedFloat `json:"price"`
	Side         string              `json:"side"`
	Size         types.StringedFloat `json:"size"`
	Time         time.Time           `json:"timestamp"`
}

type Ticker struct {
	InstrumentID string              `json:"instrument_id"`
	Last         types.StringedFloat `json:"last"`
	LastSize     types.StringedFloat `json:"last_qty"`
	Bid          types.StringedFloat `json:"best_bid"`
	Ask          types.StringedFloat `json:"best_ask"`
	BidSize      types.StringedFloat `json:"best_bid_size"`
	AskSize      types.StringedFloat `json:"best_ask_size"`

	Time time.Time `json:"timestamp"`

	//TODO there are other fields
}

// This syntax works to request historical prices
// https://ftx.com/api/markets/DEFI-PERP/trades?&start_time=1597687200&end_time=1597773600
func (req *RequestForTrades) Path() string {
	return fmt.Sprintf("/markets/%s/trades", req.ProductCode)
}

func (req *RequestForTrades) Method() string {
	return http.MethodGet
}

func (req *RequestForTrades) Query() string {
	values, _ := query.Values(req)
	return values.Encode()
}

func (req *RequestForTrades) Payload() []byte {
	return nil
}
