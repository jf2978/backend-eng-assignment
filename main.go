package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
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
	Original string   `json:"original_url"`
	Default  string   `json:"default_url"`
	Custom   string   `json:"custom_url"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	CreatedAt   time.Time `json:"created_at`
	Visits      int64     `json:"visits"`
	EncodedHist []byte    `json:"encoded_hist"`
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
		now := time.Now()
		ctx := r.Context()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var shortReq ShortUrlRequest
		if err := json.Unmarshal(body, &shortReq); err != nil {
			http.Error(w, "provided payload is not valid JSON", http.StatusBadRequest)
			return
		}

		if shortReq.Original == "" {
			http.Error(w, "required param 'url' is empty", http.StatusBadRequest)
			return
		}

		ogHash := fmt.Sprintf("%x", sha256.Sum256([]byte(shortReq.Original)))
		customHash := fmt.Sprintf("%x", sha256.Sum256([]byte(shortReq.Custom)))

		// check to see if we have record of this original url
		ogRecordId, err := rdb.Get(ctx, ogHash).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var shortUrl ShortUrl

		shortUrl.Original = shortReq.Original

		// if we do have an associated record for this url, get it
		if ogRecordId != "" {
			serialized, err := rdb.Get(ctx, ogRecordId).Result()
			if err != nil && err != redis.Nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if err := json.Unmarshal([]byte(serialized), &shortUrl); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// otherwise, generate a new url
		if err == redis.Nil {

			var suffix string
			var unique bool

			for !unique {
				b, err := generateRandomBytes(DefaultNumRandomBytes)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				suffix, err = generateRandomUrlSafeString(b)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				// check for collision (highly unlikely, but if found, let's regenerate)
				if err := rdb.Get(ctx, suffix).Err(); err != nil {
					if err != redis.Nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					unique = true
				}
			}
			shortUrl.Default = suffix
			shortUrl.Metadata.CreatedAt = now

			start, end := now, now.Add(time.Hour*24*30)
			hist := hdr.New(now.UnixMilli(), now.Add(time.Hour*24*30).UnixMilli(), 1)
			hist.SetStartTimeMs(start.UnixMilli())
			hist.SetEndTimeMs(end.UnixMilli())

			encodedHist, err := hist.Encode(hdr.V2CompressedEncodingCookieBase)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			shortUrl.Metadata.EncodedHist = encodedHist
		}

		// side effect: this will NOT overwrite an existing (hash) custom suffix
		// todo: handle data edge cases (allow multiple custom urls per record or delete existing custom hashes, etc)
		if shortReq.Custom != "" {

			// check if the custom suffix is already in use (both as someone
			// else's custom url or the unlikely case that this was a generated suffix)
			customRecordId, err := rdb.Get(ctx, customHash).Result()

			if err != nil && err != redis.Nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if customRecordId != "" && customRecordId != ogRecordId {
				http.Error(w, "custom url provided is already in use", http.StatusBadRequest)
				return
			}

			// if we either have a matching record or none at all, let's write/update our custom url data
			shortUrl.Custom = shortReq.Custom
		}

		fullRecordData, err := json.Marshal(&shortUrl)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// todo: ideally, transactionalize these for consistency
		// set the full record data (suffix -> JSON{})
		if err := rdb.Set(ctx, shortUrl.Default, string(fullRecordData), 0).Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// associate the original url (hash) -> record id (suffix)
		if err := rdb.Set(ctx, ogHash, shortUrl.Default, 0).Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// associate the custom url (hash) -> record id (suffix)
		if err := rdb.Set(ctx, customHash, shortUrl.Default, 0).Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(shortUrl)
	})
}

// RedirectHandler returns a closure responsible for
// redirecting a default or custom url to its original
func RedirectHandler(rdb *redis.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		ctx := r.Context()
		vars := mux.Vars(r)
		suffix := vars["suffix"]

		if suffix == "" {
			http.Error(w, "redirect url is empty", http.StatusBadRequest)
			return
		}

		// check if we have record of this suffix
		rec, err := rdb.Get(ctx, suffix).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var shortUrl ShortUrl

		// if we have the full record aleady, we can just redirect
		if rec != "" {
			if err := json.Unmarshal([]byte(rec), &shortUrl); err == nil {
				// update metadata before redirecting
				hist, err := hdr.Decode(shortUrl.Metadata.EncodedHist)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				if err := hist.RecordValue(now.UnixMilli()); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				fmt.Printf("recorded value: %v\n", now.UnixMilli())

				encodedHist, err := hist.Encode(hdr.V2CompressedEncodingCookieBase)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				shortUrl.Metadata.EncodedHist = encodedHist
				shortUrl.Metadata.Visits++

				serialized, err := json.Marshal(&shortUrl)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				if err := rdb.Set(ctx, shortUrl.Default, string(serialized), 0).Err(); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				http.Redirect(w, r, shortUrl.Original, http.StatusFound)
				return
			}
		}

		// otherwise, try looking for it by the custom suffix hash
		customHash := fmt.Sprintf("%x", sha256.Sum256([]byte(suffix)))
		recId, err := rdb.Get(ctx, customHash).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		rec, err = rdb.Get(ctx, recId).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.Unmarshal([]byte(rec), &shortUrl); err == nil {
			// update metadata before redirecting
			hist, err := hdr.Decode(shortUrl.Metadata.EncodedHist)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if err := hist.RecordValue(now.UnixMilli()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			fmt.Printf("recorded value: %v\n", now.UnixMilli())

			encodedHist, err := hist.Encode(hdr.V2CompressedEncodingCookieBase)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			shortUrl.Metadata.EncodedHist = encodedHist
			shortUrl.Metadata.Visits++

			serialized, err := json.Marshal(&shortUrl)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if err := rdb.Set(ctx, shortUrl.Default, string(serialized), 0).Err(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			http.Redirect(w, r, shortUrl.Original, http.StatusFound)
			return
		}

		http.Error(w, fmt.Sprintf("could not redirect url: %s\n", suffix), http.StatusNotFound)
		return
	})
}

// InfoHandler returns a closure responsible for
// returning metadata for the provided url
func InfoHandler(rdb *redis.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		vars := mux.Vars(r)
		suffix := vars["suffix"]

		if suffix == "" {
			http.Error(w, "url is empty", http.StatusBadRequest)
			return
		}

		// check if we have record of this suffix
		rec, err := rdb.Get(ctx, suffix).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var shortUrl ShortUrl

		// if we have the full record aleady, we can just get that
		if rec != "" {
			if err := json.Unmarshal([]byte(rec), &shortUrl); err == nil {
				meta := shortUrl.Metadata

				hist, err := hdr.Decode(meta.EncodedHist)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				fmt.Printf("histogram distribution: %+v\n", hist.Distribution())
				fmt.Printf("histogram distribution: %+v\n", hist.TotalCount())

				for _, v := range hist.Distribution() {
					fmt.Printf("bar: %+v\n", v.String())
				}

				// todo: print or save a snapshot of the histogram at this point
				// and return it to the client
				w.WriteHeader(http.StatusOK)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(meta)
				return
			}
		}

		// otherwise, try looking for it by the custom suffix hash
		customHash := fmt.Sprintf("%x", sha256.Sum256([]byte(suffix)))
		recId, err := rdb.Get(ctx, customHash).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		rec, err = rdb.Get(ctx, recId).Result()
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.Unmarshal([]byte(rec), &shortUrl); err == nil {
			meta := shortUrl.Metadata

			hist, err := hdr.Decode(meta.EncodedHist)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Printf("hist: %+v\n", hist)
			fmt.Printf("histogram distribution: %+v\n", hist.Distribution())

			for _, v := range hist.Distribution() {
				fmt.Printf("bar: %+v\n", v.String())
			}

			fmt.Printf("histogram distribution: %+v\n", hist.TotalCount())

			// todo: print or save a snapshot of the histogram at this point
			// and return it to the client (instead of returning the encoded one)
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(meta)
			return
		}

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
	r.Handle("/shorten/", ShortUrlHandler(rdb))
	r.Handle("/{suffix}/", RedirectHandler(rdb))
	r.Handle("/{suffix}/stats/", InfoHandler(rdb)) // todo: handle non-trailing slash

	return &App{
		context:   ctx,
		router:    r,
		dataStore: rdb,
	}
}

// generateRandomUrlSafeString will return the provided byte slice as a url-safe base64-encoded string
func generateRandomUrlSafeString(b []byte) (string, error) {
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
