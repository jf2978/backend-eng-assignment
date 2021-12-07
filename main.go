package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

const (
	DefaultAddress = "127.0.0.1:8080"
	DefaultTimeout = 10 * time.Second
)

type App struct {
	context context.Context
	router  *mux.Router
}

// GreeterHandler returns hello world with an optional name parameter
func GreeterHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	// optional name param
	name := "world"
	if val, ok := vars["name"]; ok {
		log.Printf("name: %v\n", val)
		name = val
	}

	var resp map[string]interface{}
	json.Unmarshal([]byte(fmt.Sprintf(`{ "message": "hello %s\n!" }`, name)), &resp)

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Init returns a new App with some default values
func Init() *App {
	ctx := context.Background()

	r := mux.NewRouter()
	r.HandleFunc("/", GreeterHandler)
	r.HandleFunc("/{name}/", GreeterHandler)

	return &App{
		context: ctx,
		router:  r,
	}
}

func main() {
	app := Init()

	log.Fatal(http.ListenAndServe(DefaultAddress, app.router))
}
