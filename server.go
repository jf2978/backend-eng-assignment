package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
)

const (
	DefaultAddress        = "localhost"
	ServerPort            = "8080"
	DBPort                = "6379"
	DefaultTimeout        = 10 * time.Second
	DefaultNumRandomBytes = 8
	DefaultShortBaseUrl   = "https://short.url/"
)

type Server struct {
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
	CreatedAt    time.Time `json:"created_at`
	Visits       int64     `json:"visits"`
	EncodedHist  []byte    `json:"encoded_hist"`
	Distribution string    `json:"-"` // ignore during (un)marshalling
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
			http.Error(w, "provided payload is not valid JSON", http.StatusBadRequest)
			return
		}

		if shortReq.Original == "" {
			http.Error(w, "required param 'url' is empty", http.StatusBadRequest)
			return
		}

		var shortUrl ShortUrl
		shortUrl.Original = shortReq.Original

		err = shortUrl.getByOriginal(ctx, rdb, shortReq.Original)
		if err != nil && err != redis.Nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// otherwise, generate a new url
		if err == redis.Nil {
			if err := shortUrl.generateUniqueSuffix(ctx, rdb); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if err := shortUrl.initMetadata(ctx); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		customHash := fmt.Sprintf("%x", sha256.Sum256([]byte(shortReq.Custom)))

		// side effect: this will NOT overwrite an existing custom suffix for the current record
		// i.e. a single original url can be mapped to multiple custom suffixes
		if shortReq.Custom != "" {

			// check if the custom suffix is already in use (both as someone
			// else's custom url or the unlikely case that this was a generated suffix)
			recordId, err := rdb.Get(ctx, customHash).Result()

			if err != nil && err != redis.Nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if recordId != "" && recordId != shortUrl.Default {
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
		ogHash := fmt.Sprintf("%x", sha256.Sum256([]byte(shortReq.Original)))
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
				if err := shortUrl.updateMetadata(ctx, rdb); err != nil {
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
			if err := shortUrl.updateMetadata(ctx, rdb); err != nil {
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
				if err := shortUrl.getMetadata(ctx); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusOK)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(shortUrl.Metadata)
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
			if err := shortUrl.getMetadata(ctx); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(shortUrl.Metadata)
			return
		}

	})
}

// InitServer returns a new Server with some default values
func InitServer() *Server {
	ctx := context.Background()

	// data store
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", DefaultAddress, DBPort),
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// routes
	r := mux.NewRouter()
	r.Handle("/hello", GreeterHandler())
	r.Handle("/hello/{name}/", GreeterHandler())
	r.Handle("/shorten/", ShortUrlHandler(rdb))
	r.Handle("/{suffix}/", RedirectHandler(rdb))
	r.Handle("/{suffix}/stats/", InfoHandler(rdb)) // todo: handle non-trailing slash

	return &Server{
		context:   ctx,
		router:    r,
		dataStore: rdb,
	}
}

func (s *ShortUrl) getByOriginal(ctx context.Context, rdb *redis.Client, original string) error {
	ogHash := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))

	recordId, err := rdb.Get(ctx, ogHash).Result()
	if err != nil {
		return err
	}

	if recordId != "" {
		s.Default = recordId
		serialized, err := rdb.Get(ctx, recordId).Result()
		if err != nil && err != redis.Nil {
			return err
		}

		if err := json.Unmarshal([]byte(serialized), s); err != nil {
			return err
		}
	}

	return nil
}

// generateUniqueSuffix generates a unique url-safe base64 encoded suffix for this ShortUrl
func (s *ShortUrl) generateUniqueSuffix(ctx context.Context, rdb *redis.Client) error {

	var suffix string
	var unique bool

	for !unique {
		b, err := generateRandomBytes(DefaultNumRandomBytes)
		if err != nil {
			return err
		}

		suffix, err = generateRandomUrlSafeString(b)
		if err != nil {
			return err
		}

		// check for collision (highly unlikely, but if found, let's regenerate)
		if err := rdb.Get(ctx, suffix).Err(); err != nil {
			if err != redis.Nil {
				return err
			}
			unique = true
		}
	}

	s.Default = suffix

	return nil
}

// initMetadata initializes ShortUrl metadata (namely setting created_at and the encoded histogram)
func (s *ShortUrl) initMetadata(ctx context.Context) error {
	now := time.Now()
	oneMonthFromNow := now.Add(time.Hour * 24 * 30)

	s.Metadata.CreatedAt = now

	// todo: truncate the start/end time of the histogram produced (instead of during export)
	hist := hdr.New(100000, oneMonthFromNow.Unix(), 5)
	hist.SetStartTimeMs(now.UnixMilli())
	hist.SetEndTimeMs(oneMonthFromNow.UnixMilli())

	encodedHist, err := hist.Encode(hdr.V2CompressedEncodingCookieBase)
	if err != nil {
		return err
	}

	s.Metadata.EncodedHist = encodedHist

	return nil
}

func (s *ShortUrl) getMetadata(ctx context.Context) error {
	hist, err := hdr.Decode(s.Metadata.EncodedHist)
	if err != nil {
		return err
	}

	// create/open local csv file
	f, err := os.Create("histogram.csv")
	defer f.Close()

	if err != nil {
		return err
	}

	w := csv.NewWriter(f)
	defer w.Flush()

	records := [][]string{
		{"from", "to", "count"},
	}

	// format the histogram time range & count values
	thisYear := time.Date(2021, time.January, 1, 0, 0, 0, 0, time.Local)

	for _, v := range hist.Distribution() {
		// skip over dates before this year
		if time.Unix(v.From, 0).Before(thisYear) {
			continue
		}

		from := time.Unix(v.From, 0).Format(time.RFC3339)
		to := time.Unix(v.To, 0).Format(time.RFC3339)

		record := strings.Split(fmt.Sprintf("%s,%s,%d", from, to, v.Count), ",")
		records = append(records, record)
	}

	// write the file (to buffer)
	for _, record := range records {
		if err := w.Write(record); err != nil {
			return err
		}
	}

	if err := w.Error(); err != nil {
		return err
	}

	return nil
}

// updateMetadata updates the ShortUrl visit count and histogram data
func (s *ShortUrl) updateMetadata(ctx context.Context, rdb *redis.Client) error {
	now := time.Now()
	hist, err := hdr.Decode(s.Metadata.EncodedHist)
	if err != nil {
		return err
	}

	if err := hist.RecordValue(now.Unix()); err != nil {
		return err
	}

	encodedHist, err := hist.Encode(hdr.V2CompressedEncodingCookieBase)
	if err != nil {
		return err
	}
	s.Metadata.EncodedHist = encodedHist
	s.Metadata.Visits++

	serialized, err := json.Marshal(s)
	if err != nil {
		return err
	}

	if err := rdb.Set(ctx, s.Default, string(serialized), 0).Err(); err != nil {
		return err
	}

	return nil
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
