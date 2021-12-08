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

type ShortUrl struct {
	Default string `json:"default_url"`
	Custom  string `json:"custom_url"`
	// todo: store some metadata here
}

// ShortUrlRequest represents a req
type ShortUrlRequest struct {
	Original string `json:"url"`
	Custom   string `json:"custom_suffix"`
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
// fetching or generating a shortened url for the provided original
func ShortUrlHandler(rdb *redis.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var shortReq ShortUrlRequest
		if err := json.Unmarshal(body, &shortReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		original, custom := shortReq.Original, shortReq.Custom

		var shortUrl ShortUrl
		serialized, err := rdb.Get(ctx, original).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// if we have the data for this url stored, use that
		if serialized != "" {
			if err := json.Unmarshal([]byte(serialized), &shortUrl); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// otherwise, generate a new url
		if err == redis.Nil {
			suffix, err := generateRandomUrlSafeString(DefaultNumRandomBytes)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			shortUrl.Default = fmt.Sprintf("%s%s", DefaultShortBaseUrl, suffix)
		}

		// side effect: this will overwrite an existing custom url
		// with the suffix provided in the request
		// todo: handle the case where this custom url is already in use
		if custom != "" {
			shortUrl.Custom = fmt.Sprintf("%s%s", DefaultShortBaseUrl, custom)
		}

		fmt.Printf("short url: %v\n", shortUrl)

		rawData, err := json.Marshal(&shortUrl)
		if err := rdb.Set(ctx, original, string(rawData), 0).Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(shortUrl)
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
	r.Handle("/short-url/{custom-url}/", ShortUrlHandler(rdb))

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
