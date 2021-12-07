# Range Take Home Exercise (Backend)
Hey there! I'm Jeff, and I'd love to join the engineering team at [Range](https://range.co), so here's that take-home assignment.

The intention of this file is to document some of my thinking as I as I try to understand the task, make some tradeoffs, and land on a solution.

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

### Changelog
If anything changes, or a thought comes to mind that gets me to reconsider my approach, I'll document here:

**[12/7/2021 19:17:11 (UTC)]**: wrote up this doc, thinking about first steps/approach
