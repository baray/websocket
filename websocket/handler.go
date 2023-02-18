package websocket

import (
    "context"
    "encoding/json"
    "fmt"
    "github.com/sirupsen/logrus"
    "strings"
    "sync"
    "time"
)

type Handler struct {
    w          IWriter
    ctx        context.Context
    timeout    int                      // timeout for getting request response
    requests   map[string]chan struct{} // map for handling responses for specific req_id
    reqCounter uint
    reqMutex   sync.Mutex
}

func NewHandler(writer IWriter, ctx context.Context) *Handler {
    return &Handler{
        w:        writer,
        ctx:      ctx,
        timeout:  20,  // 20 sec
        requests: make(map[string]chan struct{}),
        reqMutex: sync.Mutex{},
    }
}

func (h *Handler) Start(symbol string) error {
    err := h.authorize()
    if err != nil {
        logrus.WithError(err).Error("exit handler")
        return fmt.Errorf("authorization failed: %w", err)
    }

    err = h.subscribe(symbol)
    if err != nil {
       logrus.WithError(err).Error("exit handler")
       return fmt.Errorf("subscription failed: %w", err)
    }

    errCh := make(chan error)
    defer close(errCh)

    go h.listen(errCh)

    select {
    case err = <-errCh:
        if err != nil {
            logrus.WithError(err).Error("exit handler")
            return fmt.Errorf("process interapted with: %w", err)
        }
    case <- h.ctx.Done():
        err = h.ctx.Err()
        if err != nil {
            logrus.WithError(err).Error("exit handler")
        }
        return fmt.Errorf("exit handler: %w", err)
    }
    return nil
}

func (h *Handler) genReqID() string {
    // no need in Mutex or Atomic for now as our application sends requests in single thread
    h.reqCounter++
    return fmt.Sprintf("XXX-%d", h.reqCounter)
}

func (h *Handler) authorize() error {
    err := h.send(MethodRequest{
        ReqID:  h.genReqID(),
        Method: MethodAuth,
        Args: map[string]string{
            "login":    "foo",
            "password": "bar",
        },
    })
    if err != nil {
        logrus.WithError(err).Error("could not authorize")
        return err
    }
    return nil
}

func (h *Handler) subscribe(symbol string) error {
    err := h.send(MethodRequest{
        ReqID:  h.genReqID(),
        Method: MethodExecutions,
        Args: map[string]string{
            "symbol": symbol,
        },
    })
    if err != nil {
        logrus.WithError(err).Errorf("cannot subscribe for %s", symbol)
    }
    return nil
}

func (h *Handler) send(request MethodRequest) error {
    reqID := request.ReqID
    err := h.w.Send(request)
    if err == nil {
        ch := make(chan struct{})
        h.requests[reqID] = ch

        go func(reqID string, ch chan struct{}) {
            ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.timeout)*time.Second)
            defer cancel()
            defer close(ch)
            select {
            case <- ch:
                // got response in time
                h.reqMutex.Lock()
                defer h.reqMutex.Unlock()

                h.requests[reqID] = nil
                delete(h.requests, reqID)

            case <- ctx.Done():
                h.reqMutex.Lock()
                defer h.reqMutex.Unlock()

                h.requests[reqID] = nil
                delete(h.requests, reqID)
                logrus.Errorf("ReqID: %s failed by timout", reqID)
            }
        }(reqID, ch)
    }
    return err
}

func (h *Handler) listen(errCh chan<- error) {
    read := h.w.Read()
    for eventBytes := range read {
        event := string(eventBytes)

        // I haven't found a better way how to determine what is exactly inside the event
        if strings.Contains(event, "method") {
            // processing MethodResponse
            response := MethodResponse{}
            err := json.Unmarshal(eventBytes, &response)
            if err == nil {
                switch response.Method {
                case MethodAuthExpiring:
                    logrus.Info("refreshing token")
                    err = h.authorize()
                    if err != nil {
                        errCh <- err
                        return
                    }
                case MethodExecutions:
                    logrus.WithField("Data", response.Data).Info("got new message")
                }
            }
        } else if strings.Contains(event, "req_id") {
            // processing StatusResponse
            response := StatusResponse{}
            err := json.Unmarshal(eventBytes, &response)
            if err == nil {
                logrus.WithField("payload", event).Info("got response")
                h.reqMutex.Lock()
                resChan, ok := h.requests[response.ReqID]
                if !ok {
                    logrus.Warnf("nobody is waiting for response with ReqID=%s", response.ReqID)
                } else {
                    resChan <- struct{}{}
                }
                h.reqMutex.Unlock()
                if response.Status == false {
                    errCh <- fmt.Errorf("ReqID=%s failed with error: %s", response.ReqID, response.Error)
                    return
                }
            }
        } else {
            logrus.WithField("payload", event).Warn("unknown response type")
        }
    }

    errCh <- nil
    logrus.Info("read chan is closed")
    return
}
