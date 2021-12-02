package types

import (
	"strconv"
	"strings"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	BUY    = "buy"
	SELL   = "sell"
	MARKET = "market"
	LIMIT  = "limit"
	STOP   = "stop"
)

type StringedFloat float64

func (c *StringedFloat) UnmarshalJSON(b []byte) error {
	ot, err := strconv.ParseFloat(strings.Trim(string(b), "\""), 64)
	if err != nil {
		return err
	}

	*c = StringedFloat(ot)
	return nil
}
