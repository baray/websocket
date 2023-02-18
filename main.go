package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"ajjurtest/websocket"
)

func main() {
	ctx := context.Background()
	w := websocket.NewWriter()

	go func() {
		if err := w.Run(); err != nil {
			logrus.WithError(err).Error("stopped with error")
		}
	}()

	time.Sleep(time.Second)

	ctxCancel, cancel := context.WithCancel(ctx)
	defer cancel()
	h := websocket.NewHandler(w, ctxCancel)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
		os.Exit(1)
	}()
	_ = h.Start("BTC/USDT")
}

