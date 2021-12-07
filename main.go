package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
)

const (
	DefaultAddress        = "127.0.0.1"
	ServerPort            = "8080"
	DBPort                = "6379"
	DefaultTimeout        = 10 * time.Second
	DefaultNumRandomBytes = 8
	DefaultShortBaseUrl   = "https://short.url/"
)

type App struct {
	context   context.Context
	router    *mux.Router
	dataStore *redis.Client
}

// GreeterHandler returns a closure responsible for
// greeting the caller with an optional name parameter
func GreeterHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
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
}

func ShortUrlHandler(rdb *redis.Client) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// todo: read from request

		// todo: get existing shortened url if it exists
		val, err := rdb.Get(ctx, "key").Result()
		if err != nil && err != redis.Nil {
			// todo: handle error
		}

		// todo: otherwise set it and return it
		suffix, err := generateRandomUrlSafeString(DefaultNumRandomBytes)
		if err != nil {
			// todo: handle error
		}

		shortened := fmt.Sprintf("%s%s", DefaultShortBaseUrl, suffix)

		if err := rdb.Set(ctx, "", shortened, 0).Err(); err != nil {
			// todo: handle error
		}

		// todo: actually set response
	}
}

// Init returns a new App with some default values
func Init() *App {
	ctx := context.Background()

	// data store
	rdb := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", DefaultAddress, DBPort),
	})

	// routes
	r := mux.NewRouter()
	r.HandleFunc("/", GreeterHandler())
	r.HandleFunc("/{name}/", ShortUrlHandler(rdb))

	return &App{
		context:   ctx,
		router:    r,
		dataStore: rdb,
	}
}

// generateRandomUrlSafeString will produce n RNG bytes and return them as a url-safe base64-encoded string
// for our puposes, this means we'd generate |{set of url-safe chars}|^n possible strings
func generateRandomUrlSafeString(n int) (string, error) {
	b, err := generateRandomBytes(n)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateRandomBytes will produce n RNG bytes
func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}

	return b, nil
}

func main() {
	app := Init()

	log.Fatal(http.ListenAndServe(
		fmt.Sprintf("%s:%s", DefaultAddress, ServerPort), app.router),
	)
}
