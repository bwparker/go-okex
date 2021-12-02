package realtime

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/buger/jsonparser"
	"github.com/bwparker/go-ftx/rest/private/fills"
	"github.com/bwparker/go-ftx/rest/private/orders"
	"github.com/bwparker/go-ftx/types"
	"github.com/bwparker/go-okex/rest/public/markets"
	"github.com/gorilla/websocket"
)

const (
	UNDEFINED = iota
	ERROR
	TICKER
	TRADES
	ORDERBOOK
	ORDERS
	FILLS

	WS_ENDPOINT = "wss://real.okex.com:8443/ws/v3"
)

type request struct {
	Op   string   `json:"op"`
	Args []string `json:"args"`
}

// {"op": "login", "args": {"key": "<api_key>", "sign": "<signature>", "time": 1111}}
type requestForPrivate struct {
	Op   string                 `json:"op"`
	Args map[string]interface{} `json:"args"`
}

type Response struct {
	Type   int
	Symbol string

	Tickers   []markets.Ticker
	Trades    []markets.Trade
	Orderbook Orderbook

	Orders orders.Order
	Fills  fills.Fill

	Results error
}

type Orderbook struct {
	InstrumentID string          `json:"instrument_id"`
	Bids         FloatConversion `json:"bids"`
	Asks         FloatConversion `json:"asks"`
	// Action return update/partial
	Action   string        `json:"action"`
	Time     types.FtxTime `json:"time"`
	Checksum int           `json:"checksum"`
}

type FloatConversion [][]float64

func (c *FloatConversion) UnmarshalJSON(b []byte) error {
	tmp := [][]json.Number{}
	if err := json.Unmarshal(b, &tmp); err != nil {
		return err
	}

	*c = make(FloatConversion, len(tmp))
	for i, a := range tmp {

		var (
			pair []float64 = make([]float64, len(a))
			err  error
		)
		for j, as := range a {
			pair[j], err = as.Float64()
			if err != nil {
				return err
			}
		}

		(*c)[i] = pair
	}
	return nil
}

func subscribe(conn *websocket.Conn, channels, symbols []string) error {
	if symbols != nil {
		for i := range channels {
			for j := range symbols {
				if err := conn.WriteJSON(&request{
					Op:   "subscribe",
					Args: []string{fmt.Sprintf("%s:%s", channels[i], symbols[j])},
				}); err != nil {
					return err
				}
			}
		}
	} else {
		for i := range channels {
			if err := conn.WriteJSON(&request{
				Op:   "subscribe",
				Args: []string{fmt.Sprintf("%s", channels[i])},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func unsubscribe(conn *websocket.Conn, channels, symbols []string) error {
	if symbols != nil {
		for i := range channels {
			for j := range symbols {
				if err := conn.WriteJSON(&request{
					Op:   "unsubscribe",
					Args: []string{fmt.Sprintf("%s:%s", channels[i], symbols[j])},
				}); err != nil {
					return err
				}
			}
		}
	} else {
		for i := range channels {
			if err := conn.WriteJSON(&request{
				Op:   "unsubscribe",
				Args: []string{fmt.Sprintf("%s", channels[i])},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func ping(conn *websocket.Conn) (err error) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, []byte(`{"op": "pong"}`)); err != nil {
				goto EXIT
			}
		}
	}
EXIT:
	return err
}

func Connect(ctx context.Context, ch chan Response, channels, symbols []string, l *log.Logger) error {
	return connect(ctx, ch, channels, symbols, l, WS_ENDPOINT)
}
func connect(ctx context.Context, ch chan Response, channels, symbols []string, l *log.Logger, ep string) error {
	if l == nil {
		l = log.New(os.Stdout, "ftx websocket", log.Llongfile)
	}

	conn, _, err := websocket.DefaultDialer.Dial(ep, nil)
	if err != nil {
		return err
	}

	if err := subscribe(conn, channels, symbols); err != nil {
		return err
	}

	// ping each 15sec for exchange
	go ping(conn)

	go func() {
		defer conn.Close()
		defer unsubscribe(conn, channels, symbols)

	RESTART:
		for {
			var res Response
			_, msg, err := conn.ReadMessage()
			if err != nil {
				l.Printf("[ERROR]: msg error: %+v", err)
				res.Type = ERROR
				res.Results = fmt.Errorf("%v", err)
				ch <- res
				break RESTART
			}

			stream := flate.NewReader(bytes.NewReader(msg))
			buf := bytes.NewBuffer(nil)
			buf.ReadFrom(stream)
			msg = buf.Bytes()
			event, err := jsonparser.GetString(msg, "event")
			if err == nil {
				l.Printf("[SUCCESS]: %s %v", event, msg)
				continue
			}

			channel, err := jsonparser.GetString(msg, "table")
			if err != nil {
				l.Printf("[ERROR]: channel error: %+v", string(msg))
				res.Type = ERROR
				res.Results = fmt.Errorf("%v", string(msg))
				ch <- res
				break RESTART
			}

			data, _, _, err := jsonparser.Get(msg, "data")
			if err != nil {
				if isSubscribe, _ := jsonparser.GetString(msg, "type"); isSubscribe == "subscribed" {
					l.Printf("[SUCCESS]: %s %+v", isSubscribe, string(msg))
					continue
				} else {
					err = fmt.Errorf("[ERROR]: data err: %v %s", err, string(msg))
					l.Println(err)
					res.Type = ERROR
					res.Results = err
					ch <- res
					break RESTART
				}
			}

			switch channel {
			case "spot/ticker":
				res.Type = TICKER
				if err := json.Unmarshal(data, &res.Tickers); err != nil {
					l.Printf("[WARN]: cant unmarshal ticker %+v", err)
					continue
				}

			case "spot/trade":
				res.Type = TRADES
				if err := json.Unmarshal(data, &res.Trades); err != nil {
					l.Printf("[WARN]: cant unmarshal trades %+v", err)
					continue
				}
			case "spot/depth_l2_tbt":
				fallthrough
			case "spot/depth":
				var orderbook []Orderbook

				if err := json.Unmarshal(data, &orderbook); err != nil {
					l.Printf("[WARN]: cant unmarshal orderbook %+v", err)
					continue
				}
				for _, ob := range orderbook {
					var rs Response
					rs.Type = ORDERBOOK
					rs.Symbol = ob.InstrumentID
					rs.Orderbook = ob
					ch <- rs
				}
				continue
			default:
				res.Type = UNDEFINED
				res.Results = fmt.Errorf("%v", string(msg))
			}

			ch <- res

		}
	}()

	return nil
}

func ConnectForPrivate(ctx context.Context, ch chan Response, key, secret string, channels []string, l *log.Logger, subaccount ...string) error {
	return connectForPrivate(ctx, ch, key, secret, channels, l, WS_ENDPOINT, subaccount...)
}

func connectForPrivate(ctx context.Context, ch chan Response, key, secret string, channels []string, l *log.Logger, ep string, subaccount ...string) error {
	if l == nil {
		l = log.New(os.Stdout, "ftx websocket", log.Llongfile)
	}

	conn, _, err := websocket.DefaultDialer.Dial(ep, nil)
	if err != nil {
		return err
	}

	// sign up
	if err := signature(conn, key, secret, subaccount); err != nil {
		return err
	}

	if err := subscribe(conn, channels, nil); err != nil {
		return err
	}

	go ping(conn)

	go func() {
		defer conn.Close()
		defer unsubscribe(conn, channels, nil)

	RESTART:
		for {
			var res Response
			_, msg, err := conn.ReadMessage()
			if err != nil {
				l.Printf("[ERROR]: msg error: %+v", err)
				res.Type = ERROR
				res.Results = fmt.Errorf("%v", err)
				ch <- res
				break RESTART
			}

			typeMsg, err := jsonparser.GetString(msg, "type")
			if typeMsg == "error" {
				l.Printf("[ERROR]: error: %+v", string(msg))
				res.Type = ERROR
				res.Results = fmt.Errorf("%v", string(msg))
				ch <- res
				break RESTART
			}

			channel, err := jsonparser.GetString(msg, "channel")
			if err != nil {
				l.Printf("[ERROR]: channel error: %+v", string(msg))
				res.Type = ERROR
				res.Results = fmt.Errorf("%v", string(msg))
				ch <- res
				break RESTART
			}

			data, _, _, err := jsonparser.Get(msg, "data")
			if err != nil {
				if isSubscribe, _ := jsonparser.GetString(msg, "type"); isSubscribe == "subscribed" {
					l.Printf("[SUCCESS]: %s %+v", isSubscribe, string(msg))
					continue
				} else {
					err = fmt.Errorf("[ERROR]: data err: %v %s", err, string(msg))
					l.Println(err)
					res.Type = ERROR
					res.Results = err
					ch <- res
					break RESTART
				}
			}

			// Private channel has not market name.
			switch channel {
			case "orders":
				res.Type = ORDERS
				if err := json.Unmarshal(data, &res.Orders); err != nil {
					l.Printf("[WARN]: cant unmarshal orders %+v", err)
					continue
				}

			case "fills":
				res.Type = FILLS
				if err := json.Unmarshal(data, &res.Fills); err != nil {
					l.Printf("[WARN]: cant unmarshal fills %+v", err)
					continue
				}

			default:
				res.Type = UNDEFINED
				res.Results = fmt.Errorf("%v", string(msg))
			}

			ch <- res
		}
	}()

	return nil
}

func signature(conn *websocket.Conn, key, secret string, subaccount []string) error {
	// key: your API key
	// time: integer current timestamp (in milliseconds)
	// sign: SHA256 HMAC of the following string, using your API secret: <time>websocket_login
	// subaccount: (optional) subaccount name
	// As an example, if:

	// time: 1557246346499
	// secret: 'Y2QTHI23f23f23jfjas23f23To0RfUwX3H42fvN-'
	// sign would be d10b5a67a1a941ae9463a60b285ae845cdeac1b11edc7da9977bef0228b96de9

	// One websocket connection may be logged in to at most one user. If the connection is already authenticated, further attempts to log in will result in 400s.

	msec := time.Now().UTC().UnixNano() / int64(time.Millisecond)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%dwebsocket_login", msec)))
	args := map[string]interface{}{
		"key":  key,
		"sign": hex.EncodeToString(mac.Sum(nil)),
		"time": msec,
	}
	if len(subaccount) > 0 {
		args["subaccount"] = subaccount[0]
	}

	if err := conn.WriteJSON(&requestForPrivate{
		Op:   "login",
		Args: args,
	}); err != nil {
		return err
	}

	return nil
}
