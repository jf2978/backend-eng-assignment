# Take Home Exercise (Backend)

## Problem
Write a server that acts as a bit.ly-like URL shortener (as a JSON API)

### Goals

- Be able to generate arbitrary shortened urls (e.g. bit.ly/2FhfhXh) such that:
  - the same input url outputs the same shortened url (i.e. it's deterministic)
- Be able to create a custom short link urls (e.g., bit.ly/my-custom-link)
- Handle the actual redirect from short url → original url
- Be able to return the following stats about a given short url to the api consumer:
  - When the link was created
  - Total number of visits
  - A histogram of the total number of visits per day

### Future Goals (Extra Credit)

- Deploy the server to an accessible URL
- Number of “unique” visitors to a given shortlink (you can define how we track visitors)
- A “global stats” endpoint, that aggregates some interesting stats across your URL-shortening platform (e.g., total number of links/visits per domain, histogram of total visits across the site)

### Non-Goals

- Try to predict/foresee what features we'd want to add to this system
- Handle more than one custom url (could be a future goal)

## Design

### Overview

- **Programming Language**: Go
- **Libraries**: There are web frameworks like [gin](https://github.com/gin-gonic/gin) and [echo](https://github.com/labstack/echo), but that seems like. I'd rather stick to more lightweight router library to keep things simple, minimize dependencies, etc. Let's try [gorilla/mux](https://github.com/gorilla/mux)
  - Note: Of course there are other libraries I'll find handy too for things like CLI flag parsing, error handling, etc. but no need to flesh those out here
- **Data Store**: Don't have much experience in NoSQL databases, but a KV store feels like a natural fit (the original url as key, json blob as value). I've always wanted to tinker with Redis, so I'll start with [this](https://github.com/go-redis/redis) db driver (over others mainly because the repo seems more active)

## Sample Usage

### Setup

```bash
## run local redis server (reset if necessary)
redis-cli flushall
redis-server

## run the server (new tab) localhost:8080
go run .

```

### Shortening Some URLs

```bash

## create a short url at localhost:8080
curl -d '{"url": "https://google.com/", "custom_suffix": "goog"}' -H 'Content-Type: application/json' localhost:8080/shorten/

{
    "original_url": "https://google.com/",
    "default_url": "cwe8WeI6myY",
    "custom_url": "goog",
    "metadata": {
        "CreatedAt": "2021-12-13T17:58:06.578754-05:00",
        "visits": 7,
        "encoded_hist": "SElTVEZBQUFBREY0MnBKcG1Tek13TURBd3NEQXdNakF3TURLQUFLTWJRdEFWT0w5bUgvMkg4QWlERy9ibVBrQUFRQUEvLytIL3dmRg=="
    }
}

## go to the url in browser (or directly using curl)
curl localhost:8080/goog/

<a href="https://google.com/">Found</a>.

curl localhost:8080/cwe8WeI6myY/
<a href="https://google.com/">Found</a>.

```

### Getting Some Metadata

```bash

## pretty some basic stats (histogram is an encoded object)
curl localhost:8080/cwe8WeI6myY/stats/

{
    "created_at": "0001-01-01T00:00:00Z",
    "visits": 9,
    "encoded_hist": "SElTVEZBQUFBREY0MnBKcG1Tek13TURBd3NEQXdNakF3TURLQUFLTWJRdEFWT0w5bUgvMkg4QWlERy9ibUlVQUFRQUEvLytJQXdmSg=="
}

## see histogram as csv in stdout (for easy import elsewhere)

cat histogram.csv

[...]
2021-12-07T19:44:48-05:00,2021-12-08T13:57:03-05:00,0
2021-12-08T13:57:04-05:00,2021-12-09T08:09:19-05:00,0
2021-12-09T08:09:20-05:00,2021-12-10T02:21:35-05:00,0
2021-12-10T02:21:36-05:00,2021-12-10T20:33:51-05:00,0
2021-12-10T20:33:52-05:00,2021-12-11T14:46:07-05:00,0
2021-12-11T14:46:08-05:00,2021-12-12T08:58:23-05:00,0
2021-12-12T08:58:24-05:00,2021-12-13T03:10:39-05:00,0
2021-12-13T03:10:40-05:00,2021-12-13T21:22:55-05:00,9

```
