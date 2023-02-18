package websocket

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
func (MethodResponse) ImplementsMessage() {}

type StatusResponse struct {
    ReqID  string `json:"req_id"`
    Status bool   `json:"status"`
    Error  string `json:"error"`
}
func (StatusResponse) ImplementsMessage() {}

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
    out chan IMessage

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
        out:         make(chan IMessage, 10),
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
                    "price":  fmt.Sprintf("%d", time.Now().Unix()),
                    "amount": "105",
                },
            }
        }
    }

}

// Send отправляет сообщение по WebSocket. Для упрощения реализация принимает сразу MethodRequest вместо []byte
func (w *Writer) Send(m MethodRequest) error {
    select {
    case <-w.ctx.Done():
        return ErrDisconnected
    default:

    }

    // TODO: писать в w.out не безопасно
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
                    Error:  "this method isn'timeout accesible without authorization",
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