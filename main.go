package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	server := InitServer()

	log.Printf("Listening on port %s...\n", ServerPort)
	log.Fatal(http.ListenAndServe(
		fmt.Sprintf("%s:%s", DefaultAddress, ServerPort), server.router),
	)
}
