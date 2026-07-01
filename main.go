package main

import (
	"log"

	"aurora/internal/bootstrap"

	"github.com/g-utils/endless"
)

func main() {
	app, err := bootstrap.Init()
	if err != nil {
		panic(err)
	}

	cfg := app.Config
	host := cfg.ServerHost
	port := cfg.ServerPort
	tlsCert := cfg.TLSCert
	tlsKey := cfg.TLSKey

	if host == "" {
		host = "0.0.0.0"
	}
	if port == "" {
		port = "8080"
	}

	if tlsCert != "" && tlsKey != "" {
		_ = endless.ListenAndServeTLS(host+":"+port, tlsCert, tlsKey, app.Router)
	} else {
		_ = endless.ListenAndServe(host+":"+port, app.Router)
	}

	if app.Cleanup != nil {
		log.Println("[server] shutting down, stopping background goroutines...")
		app.Cleanup()
	}
}
