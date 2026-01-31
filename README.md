# retry-strategies-simulator

A Go simulation that shows how different retry strategies affect server recovery after an outage. Run it and see the results as an ASCII histogram in your terminal.

## What it does

- A mock server handles 200 requests per second. It's down for the first 10 seconds.
- 1000 clients all fail during the outage, then retry using one of 4 strategies.
- The output shows requests per second as a bar chart, plus summary metrics.

## Strategies

- **constant** - fixed 1ms delay, no backoff (worst case)
- **backoff** - exponential backoff, 100ms base, 10s cap
- **jitter** - random delay between 0 and the exponential backoff value
- **decorrelated** - each delay is random between base and 3× the previous delay

## How to run

```bash
go run main.go -strategy=constant
go run main.go -strategy=backoff
go run main.go -strategy=jitter
go run main.go -strategy=decorrelated
```

Requires Go 1.21 or later. No external dependencies.


