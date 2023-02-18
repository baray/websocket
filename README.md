# A mock WebSocket server

// There is a WebSocket server that you need to integrate with.

// It has its own protocol with a certain logic.

// There are three types of messages:

// MethodRequest Client request to the server
type MethodRequest struct {
  // ReqID Unique within the ID sessions for subsequent matching of the response to the request
	ReqID  string            `json:"req_id"`
  // Method The name of the method that is called
	Method string            `json:"method"`
  // Args Arguments for calling the method
	Args   map[string]string `json:"args"`
}

// StatusReponse response from the server to MethodRequest
type StatusResponse struct {
  // ReqID For matching between the request <-> answer
	ReqID  string `json:"req_id"`
  // Status Signals the success of the subscription
	Status bool   `json:"status"`
  // Error Details of the error, if Status = false
	Error  string `json:"error"`
}

// MethodResponse Streaming messages after a successful subscription
type MethodResponse struct {
	Method string            `json:"method"`
	Data   map[string]string `json:"data"`
}

// The following values can be for the Method field:

// MethodAuth="auth" - authorization (client, server)
// MethodExecutions="executions" - updates by some character (client, server)
// MethodAuthExpiring="authExpiring" - signals that the authorization token is faded and you need to log in again (server)

// The task comes with a mock instead of a real WebSocket server:

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	MethodAuth         = "auth"
	MethodAuthExpiring = "authExpiring"
	MethodExecutions   = "executions"
)

type MethodResponse struct {
	Method string            `json:"method"`
	Data   map[string]string `json:"data"`
}

type StatusResponse struct {
	ReqID  string `json:"req_id"`
	Status bool   `json:"status"`
	Error  string `json:"error"`
}

type MethodRequest struct {
	ReqID  string            `json:"req_id"`
	Method string            `json:"method"`
	Args   map[string]string `json:"args"`
}

type authState struct {
	authorized bool
	at         time.Time
}

type Writer struct {
	out chan interface{}

	subMu      sync.RWMutex
	subscribed map[string]struct{}

	ctx         context.Context
	ctxCancelFn func()

	authMu sync.RWMutex
	auth   authState
}

var (
	ErrDisconnected = fmt.Errorf("disconnected")
)

func NewWriter() *Writer {
	ctx, cancel := context.WithCancel(context.Background())

	return &Writer{
		out:         make(chan interface{}, 10),
		subscribed:  make(map[string]struct{}),
		ctx:         ctx,
		ctxCancelFn: cancel,
	}
}

func (w *Writer) streamSymbol(symbol string) {
	tk := time.NewTicker(time.Millisecond * 500)
	defer tk.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-tk.C:
			w.out <- MethodResponse{
				Method: MethodExecutions,
				Data: map[string]string{
					"symbol": symbol,
					"price":  fmt.Sprintf("%d", time.Now().UnixMilli()),
					"amount": "105",
				},
			}
		}
	}

}

// Send a message via WebSocket. To simplify, the implementation accepts MethodRequest at once instead of []byte
func (w *Writer) Send(m MethodRequest) error {
	select {
	case <-w.ctx.Done():
		return ErrDisconnected
	default:

	}

	// TODO: Writing to w.out is not safe
	switch m.Method {
	case MethodExecutions:
		symbol := m.Args["symbol"]

		w.subMu.RLock()
		_, ok := w.subscribed[symbol]
		w.subMu.RUnlock()
		if !ok {

			w.authMu.RLock()
			isAuth := w.auth.authorized
			w.authMu.RUnlock()

			if !isAuth {
				w.out <- StatusResponse{
					ReqID:  m.ReqID,
					Status: false,
					Error:  "this method isn't accesible without authorization",
				}

				return nil
			}

			w.subMu.Lock()
			w.subscribed[symbol] = struct{}{}
			w.subMu.Unlock()

			w.out <- StatusResponse{
				ReqID:  m.ReqID,
				Status: true,
				Error:  "",
			}

			go w.streamSymbol(symbol)

			return nil
		}

		w.out <- StatusResponse{
			ReqID:  m.ReqID,
			Status: false,
			Error:  "already subscribed",
		}

	case MethodAuth:
		if err := w.authCheck(m); err == nil {

			w.authMu.Lock()
			w.auth = authState{
				authorized: true,
				at:         time.Now(),
			}
			w.authMu.Unlock()

			w.out <- StatusResponse{
				ReqID:  m.ReqID,
				Status: true,
				Error:  "",
			}

		} else {
			w.out <- StatusResponse{
				ReqID:  m.ReqID,
				Status: false,
				Error:  err.Error(),
			}
		}

	default:
		// just ignore
		return nil
	}

	return nil
}

func (w *Writer) Read() <-chan []byte {
	byteCh := make(chan []byte)

	go func() {
		defer close(byteCh)

		for {
			select {
			case item := <-w.out:

				b, err := json.Marshal(item)
				if err != nil {
					fmt.Printf("could not marshal object: %v\n", err)
				}

				byteCh <- b
			case <-w.ctx.Done():
				return
			}
		}
	}()

	return byteCh
}

func (w *Writer) authCheck(a MethodRequest) error {
	if a.Method != MethodAuth {
		return fmt.Errorf("incorrect method: %s", a.Method)
	}

	if a.Args["login"] != "foo" || a.Args["password"] != "bar" {
		return fmt.Errorf("wrong creds")
	}

	return nil
}

func (w *Writer) Stop() {
	w.ctxCancelFn()

	// close(w.out)
}

func (w *Writer) Run() error {
	tkExpiring := time.NewTicker(time.Second * 10)
	defer tkExpiring.Stop()

	for {
		select {
		case <-tkExpiring.C:
			w.authMu.RLock()
			auth := w.auth
			w.authMu.RUnlock()

			if !auth.authorized {
				continue
			}

			timeDiff := time.Now().Sub(auth.at)
			if timeDiff > time.Minute {
				w.Stop()
				return ErrDisconnected
			}

			if timeDiff > time.Second*20 {
				w.out <- MethodResponse{
					Method: MethodAuthExpiring,
				}
			}
		}
	}
}


// Example of working with the server:
package main

import (
	"time"

	"github.com/sirupsen/logrus"
)

func main() {
	w := NewWriter()

	go func() {
		if err := w.Run(); err != nil {
			logrus.WithError(err).Error("stopped with error")
		}
	}()

	time.Sleep(time.Second)

	err := w.Send(MethodRequest{
		ReqID:  "XXX-1",
		Method: MethodAuth,
		Args: map[string]string{
			"login":    "foo",
			"password": "bar",
		},
	})
	if err != nil {
		logrus.WithError(err).Error("could not authorize")
	}

	err = w.Send(MethodRequest{
		ReqID:  "XXX-2",
		Method: MethodExecutions,
		Args: map[string]string{
			"symbol": "BTC/USDT",
		},
	})
	if err != nil {
		logrus.WithError(err).Error("could not authorize")
	}

	read := w.Read()
	for event := range read {
		logrus.WithField("payload", string(event)).Info("got new message")
	}
}

// Logic of the mok server

// The server is waiting for valid authorization. You can't subscribe to other methods without authorization.

// After successful authorization, every 10 seconds, it starts a token lifetime check

// If the token fades soon (more than 20 seconds have passed), it starts sending MethodAuthExpiring messages

// If the user is not re-authorized within a minute, the token is considered rotten and the user is disconnected.

// When subscribing, MethodExecutions sends events for the transmitted symbol parameter every 500ms.
