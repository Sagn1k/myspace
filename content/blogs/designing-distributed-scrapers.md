---
title: "Designing Distributed Web Scrapers in Go"
date: 2026-01-15
tags: ["go", "distributed-systems", "web-scraping", "concurrency", "architecture"]
description: "A deep dive into building scalable, fault-tolerant web scrapers using Go's concurrency primitives, message queues, and worker pools."
status: published
---

## Why Distributed Scraping?

If you've ever tried to scrape more than a few thousand pages with a single-process scraper, you've hit the wall. Rate limits, memory pressure, network timeouts, and sheer volume make single-node scraping impractical at scale. In this post, I'll walk through the architecture I used to build a distributed scraping system in Go that handles tens of millions of URLs per day across a fleet of workers.

The key insight is that scraping is an embarrassingly parallel problem — each URL fetch is independent — but the coordination layer (deduplication, rate limiting, retry logic, result aggregation) is where all the complexity lives.

## High-Level Architecture

The system breaks down into four components:

- **Scheduler** — Accepts seed URLs, manages the crawl frontier, and enforces per-domain rate limits.
- **Workers** — Stateless processes that pull tasks from a queue, fetch pages, extract data, and push results back.
- **Queue** — A message broker (we used NATS JetStream) that decouples the scheduler from workers.
- **Storage** — A combination of PostgreSQL for structured data and S3-compatible object storage for raw HTML.

This separation means you can scale workers horizontally without touching the scheduler, and you can swap out the queue or storage backend independently.

## The Worker Pool

Each worker runs a pool of goroutines managed by a semaphore pattern. Here's the core loop:

```go
func (w *Worker) Run(ctx context.Context) error {
    sem := make(chan struct{}, w.concurrency)

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        msg, err := w.queue.Pull(ctx, "scrape.tasks")
        if err != nil {
            w.logger.Warn("queue pull failed", "error", err)
            time.Sleep(time.Second)
            continue
        }

        sem <- struct{}{}
        go func(m *nats.Msg) {
            defer func() { <-sem }()
            w.processTask(ctx, m)
        }(msg)
    }
}
```

The `concurrency` parameter controls how many in-flight requests each worker handles. We typically set this to 50-100 per worker, depending on the target site's tolerance. Each worker process consumes about 30-60 MB of memory at that concurrency level, making it easy to pack many workers onto a single machine.

### Task Processing

Each task is a simple JSON payload containing the URL, depth, and metadata:

```go
type ScrapeTask struct {
    URL       string            `json:"url"`
    Depth     int               `json:"depth"`
    MaxDepth  int               `json:"max_depth"`
    Headers   map[string]string `json:"headers,omitempty"`
    CreatedAt time.Time         `json:"created_at"`
}
```

The `processTask` function handles the full lifecycle: fetch, parse, extract links, store results, and optionally enqueue discovered URLs back into the frontier.

```go
func (w *Worker) processTask(ctx context.Context, msg *nats.Msg) {
    var task ScrapeTask
    if err := json.Unmarshal(msg.Data, &task); err != nil {
        msg.Nak()
        return
    }

    resp, err := w.client.Do(ctx, task.URL, task.Headers)
    if err != nil {
        w.handleFetchError(task, err)
        msg.Nak()
        return
    }
    defer resp.Body.Close()

    doc, err := goquery.NewDocumentFromReader(resp.Body)
    if err != nil {
        msg.Nak()
        return
    }

    result := w.extractor.Extract(doc, task.URL)
    w.store.Save(ctx, result)

    if task.Depth < task.MaxDepth {
        for _, link := range result.Links {
            w.queue.Publish("scrape.tasks", ScrapeTask{
                URL:      link,
                Depth:    task.Depth + 1,
                MaxDepth: task.MaxDepth,
            })
        }
    }

    msg.Ack()
}
```

## Rate Limiting Per Domain

One of the trickier parts is enforcing per-domain rate limits across multiple workers. We solved this with a centralized rate limiter backed by Redis. Each worker checks the limiter before making a request:

```go
func (rl *RedisRateLimiter) Allow(ctx context.Context, domain string) (bool, time.Duration) {
    key := fmt.Sprintf("ratelimit:%s", domain)
    count, err := rl.client.Incr(ctx, key).Result()
    if err != nil {
        return true, 0 // fail open
    }
    if count == 1 {
        rl.client.Expire(ctx, key, rl.window)
    }
    if count > int64(rl.maxRequests) {
        ttl, _ := rl.client.TTL(ctx, key).Result()
        return false, ttl
    }
    return true, 0
}
```

When a request is denied, the worker re-enqueues the task with a delay. This keeps the system polite even under heavy load.

## Deduplication

We use a Bloom filter for fast URL deduplication. The scheduler maintains the filter in memory and checks every incoming URL before enqueuing it. At 100 million URLs with a 0.1% false positive rate, the filter only needs about 120 MB of memory — a reasonable trade-off.

For persistence across restarts, the Bloom filter state is periodically serialized and stored in Redis using `DUMP` and `RESTORE`.

## Observability

Every component exports Prometheus metrics:

- `scraper_tasks_processed_total` — counter per worker, labeled by status (success, error, rate_limited)
- `scraper_fetch_duration_seconds` — histogram of HTTP request durations
- `scraper_queue_depth` — gauge showing pending tasks in the queue
- `scraper_domains_active` — gauge of unique domains being scraped

These metrics feed into Grafana dashboards that give real-time visibility into throughput, error rates, and queue backpressure.

## Lessons Learned

After running this system in production for several months, a few takeaways:

- **Respect robots.txt.** Parse it, cache it, and honor it. It's both ethical and practical — sites that detect violations will block your IP ranges aggressively.
- **Use headless browsers sparingly.** We only route JavaScript-heavy pages through a Chromium pool. The overhead is 10-50x compared to plain HTTP fetches.
- **Idempotency matters.** Workers will crash, messages will be redelivered. Every operation must be safe to retry.
- **Monitor queue depth obsessively.** A growing queue means your workers can't keep up, and backpressure will cascade through the system.

Distributed scraping isn't conceptually difficult, but the devil is in the operational details. Go's goroutines and channels make the concurrency model natural, and the ecosystem around NATS, Redis, and PostgreSQL provides solid building blocks for the coordination layer.
