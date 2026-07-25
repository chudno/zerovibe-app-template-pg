// Command server — локальный вход приложения: HTTP-сервер с graceful shutdown.
// Используется при разработке и в песочнице агента. Сборка слоёв — в
// internal/app (общая с входом серверлесс-функции, см. handler.go в корне).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chudno/zerovibe/internal/app"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	handler, cleanup, err := app.Build(context.Background())
	if err != nil {
		return err
	}
	defer cleanup()

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// graceful shutdown по SIGINT/SIGTERM
	go func() {
		log.Printf("zerovibe слушает %s (db=postgres)", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("остановка...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
