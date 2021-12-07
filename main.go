package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
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

type ShortUrlRequest struct {
	Url string `json:"url"`
}

// GreeterHandler returns a closure responsible for
// greeting the caller with an optional name parameter
func GreeterHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
}

// ShortUrlHandler returns a closure responsible for
func ShortUrlHandler(rdb *redis.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			// todo: handle error
			panic(err)
		}

		fmt.Printf("body: %v\n", body)
		fmt.Printf("body: %v\n", string(body))

		var shortReq ShortUrlRequest
		if err := json.Unmarshal(body, &shortReq); err != nil {
			// todo: handle error
			panic(err)
		}

		fmt.Printf("shortReq: %+v\n", shortReq)

		original := shortReq.Url

		// get existing shortened url if it exists
		fmt.Printf("redis db: %+v\n", rdb.Options().Addr)

		val, err := rdb.Get(ctx, original).Result()
		if err != nil && err != redis.Nil {
			// todo: handle error
			panic(err)
		}

		fmt.Printf("val: %s\n", val)

		// otherwise, generate and set it
		if err == redis.Nil {
			suffix, err := generateRandomUrlSafeString(DefaultNumRandomBytes)
			if err != nil {
				// todo: handle error
				panic(err)
			}

			val = fmt.Sprintf("%s%s", DefaultShortBaseUrl, suffix)
			if err := rdb.Set(ctx, original, val, 0).Err(); err != nil {
				// todo: handle error
				panic(err)
			}
		}

		var resp map[string]interface{}
		json.Unmarshal([]byte(fmt.Sprintf(`{ "shortened": "%s" }`, val)), &resp)

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
}

// Init returns a new App with some default values
func Init() *App {
	ctx := context.Background()

	// data store
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", "localhost", DBPort),
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	fmt.Printf("redis db: %+v\n", rdb.Options().Addr)

	// routes
	r := mux.NewRouter()
	r.Handle("/hello", GreeterHandler())
	r.Handle("/hello/{name}/", GreeterHandler())
	r.Handle("/short-url/", ShortUrlHandler(rdb))

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

	log.Printf("Listening on port %s...\n", ServerPort)
	log.Fatal(http.ListenAndServe(
		fmt.Sprintf("%s:%s", DefaultAddress, ServerPort), app.router),
	)
}
