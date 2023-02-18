package websocket

type IWriter interface {
    Send(m MethodRequest) error
    Read() <-chan []byte
    Stop()
    Run() error
}

type IMessage interface {
    ImplementsMessage()
}
