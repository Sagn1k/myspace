---
title: "PostgreSQL Performance Tuning: From Slow Queries to Sub-Millisecond Responses"
date: 2026-03-01
tags: ["postgresql", "databases", "performance", "sql", "indexing", "backend"]
description: "Practical techniques for diagnosing and fixing slow PostgreSQL queries, covering EXPLAIN plans, indexing strategies, query rewrites, and configuration tuning."
status: published
---

## The Performance Debugging Mindset

PostgreSQL is remarkably fast when used well and painfully slow when used poorly. The difference almost always comes down to whether the query planner can find an efficient execution path. Your job as a developer isn't to outsmart the planner — it's to give it the information and indexes it needs to make good decisions.

In this post, I'll walk through real-world patterns I've used to take queries from seconds down to milliseconds. Everything here applies to PostgreSQL 15+ and has been tested under production workloads.

## Start with EXPLAIN ANALYZE

Before changing anything, you need to understand what PostgreSQL is actually doing. `EXPLAIN` shows the planned execution. `EXPLAIN ANALYZE` executes the query and shows actual timings. Always use the latter for real debugging:

```sql
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT o.id, o.total, c.name
FROM orders o
JOIN customers c ON c.id = o.customer_id
WHERE o.status = 'pending'
  AND o.created_at > now() - interval '7 days'
ORDER BY o.created_at DESC
LIMIT 50;
```

The `BUFFERS` option is critical — it tells you how many pages were read from cache vs. disk. A query that reads 100,000 buffer pages to return 50 rows has an indexing problem.

### Reading the Output

Here's a simplified plan for a slow version of the query above:

```
Sort  (cost=45321.12..45321.15 rows=50 width=64) (actual time=1823.441..1823.445 rows=50 loops=1)
  Sort Key: o.created_at DESC
  ->  Hash Join  (cost=1234.56..45310.89 rows=8234 width=64) (actual time=12.331..1821.007 rows=8234 loops=1)
        Hash Cond: (o.customer_id = c.id)
        ->  Seq Scan on orders o  (cost=0.00..43210.00 rows=8234 width=48) (actual time=0.021..1805.332 rows=8234 loops=1)
              Filter: (status = 'pending' AND created_at > ...)
              Rows Removed by Filter: 4891766
        Buffers: shared hit=12044 read=31200
```

The smoking gun is `Seq Scan on orders` with `Rows Removed by Filter: 4891766`. PostgreSQL is reading nearly 5 million rows to find 8,234 matches. That's a full table scan.

## Indexing Strategies

### Composite Indexes

The most impactful optimization for the query above is a composite index that covers both filter conditions:

```sql
CREATE INDEX idx_orders_status_created
ON orders (status, created_at DESC);
```

Column order matters. Put the equality condition (`status = 'pending'`) first and the range condition (`created_at >`) second. This lets PostgreSQL seek directly to the `pending` entries and scan forward through the date range.

After adding this index, the plan changes dramatically:

```
Limit  (cost=0.56..123.45 rows=50 width=64) (actual time=0.087..0.312 rows=50 loops=1)
  ->  Nested Loop  (cost=0.56..2345.67 rows=8234 width=64) (actual time=0.085..0.305 rows=50 loops=1)
        ->  Index Scan using idx_orders_status_created on orders o  ...
              Index Cond: (status = 'pending' AND created_at > ...)
              Buffers: shared hit=4
```

From 1.8 seconds to 0.3 milliseconds. The index scan reads exactly the rows it needs, and because of the `DESC` ordering in the index, the `ORDER BY` + `LIMIT` can stop after reading 50 rows.

### Partial Indexes

If only 2% of your orders are `pending`, a partial index is even better:

```sql
CREATE INDEX idx_orders_pending_created
ON orders (created_at DESC)
WHERE status = 'pending';
```

This index is dramatically smaller because it only contains rows matching the `WHERE` clause. Smaller index means more of it fits in cache, which means faster lookups.

### Covering Indexes (INCLUDE)

If the query only needs columns that are in the index, PostgreSQL can satisfy the query entirely from the index — an index-only scan. Use `INCLUDE` to add non-searchable columns:

```sql
CREATE INDEX idx_orders_pending_covering
ON orders (created_at DESC)
INCLUDE (total, customer_id)
WHERE status = 'pending';
```

Now the planner doesn't need to visit the heap (table) at all for the initial filter and sort. The join to `customers` still requires a heap lookup on that table, but you've eliminated the expensive part.

## Common Query Anti-Patterns

### Wrapping Indexed Columns in Functions

This kills index usage:

```sql
-- BAD: function on the indexed column
SELECT * FROM events
WHERE date_trunc('day', created_at) = '2026-01-15';

-- GOOD: range scan on the raw column
SELECT * FROM events
WHERE created_at >= '2026-01-15'
  AND created_at < '2026-01-16';
```

The first query applies `date_trunc` to every row, making the index on `created_at` useless. The second query uses a simple range condition that maps directly to an index scan.

### SELECT * with Large Rows

Fetching all columns when you only need three forces PostgreSQL to read the full row from the heap, even if an index-only scan would otherwise be possible. Always select only the columns you need:

```sql
-- BAD
SELECT * FROM products WHERE category_id = 42;

-- GOOD
SELECT id, name, price FROM products WHERE category_id = 42;
```

### N+1 in Disguise

ORMs love to generate queries like this in a loop:

```sql
SELECT * FROM orders WHERE customer_id = 1;
SELECT * FROM orders WHERE customer_id = 2;
SELECT * FROM orders WHERE customer_id = 3;
-- ... 997 more
```

Each query is fast, but a thousand round trips add up. Use a single query with `IN` or a join instead.

## Configuration Tuning

The default PostgreSQL configuration is designed to run on a Raspberry Pi. For a production server, at minimum adjust these settings:

- **`shared_buffers`** — Set to 25% of total RAM. This is PostgreSQL's main cache.
- **`effective_cache_size`** — Set to 50-75% of total RAM. This tells the planner how much data it can expect to find cached by the OS.
- **`work_mem`** — Memory per sort/hash operation. Default is 4 MB, which forces large sorts to spill to disk. Set to 64-256 MB depending on your workload and connection count.
- **`random_page_cost`** — Default is 4.0, which assumes spinning disks. On SSDs, set to 1.1-1.5. This makes the planner more willing to use index scans.
- **`maintenance_work_mem`** — Memory for `CREATE INDEX`, `VACUUM`, etc. Set to 512 MB-1 GB for faster maintenance operations.

```ini
# postgresql.conf for a 64GB RAM server on SSDs
shared_buffers = 16GB
effective_cache_size = 48GB
work_mem = 128MB
random_page_cost = 1.1
maintenance_work_mem = 1GB
wal_buffers = 64MB
checkpoint_completion_target = 0.9
```

### Connection Pooling

Each PostgreSQL connection consumes about 5-10 MB of memory and a slot in the process table. If your application opens 500 connections, that's 5 GB of overhead before you've cached a single page. Use PgBouncer or the built-in connection pooling in your application framework, and keep `max_connections` reasonable (100-200 for most workloads).

## Monitoring Ongoing Performance

Install `pg_stat_statements` and query it regularly to find your slowest and most-called queries:

```sql
SELECT
    calls,
    round(total_exec_time::numeric, 2) AS total_ms,
    round(mean_exec_time::numeric, 2) AS avg_ms,
    query
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20;
```

This single view has been responsible for more performance wins in my experience than any other tool. It tells you exactly where your database is spending its time, and the fix is usually an obvious missing index or a query that needs rewriting.

## Conclusion

PostgreSQL performance tuning follows a repeatable process: identify slow queries with `pg_stat_statements`, understand them with `EXPLAIN ANALYZE`, fix them with appropriate indexes and query rewrites, and verify with metrics. The tools are all built into the database — you just need to know where to look.
