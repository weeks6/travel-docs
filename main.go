package main

import (
	"log"
	"net/http"
	"time"

	"github.com/weeks6/travel-docs/services/auth"
)

func main() {
	s := &http.Server{
		Addr:           ":8080",
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	auth.InitJWT()
	auth.RegisterHandlers()

	log.Fatal(s.ListenAndServe())
}
