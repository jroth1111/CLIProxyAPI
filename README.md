# CLIProxyAPI Plus

English | [Chinese](README_CN.md)

This is the Plus version of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI), adding support for third-party providers on top of the mainline project.

All third-party provider support is maintained by community contributors; CLIProxyAPI does not provide technical support. Please contact the corresponding community maintainer if you need assistance.

The Plus release stays in lockstep with the mainline features.

## Operational Enhancements

This fork includes additional "proxy ops" features beyond the mainline release to improve third-party provider integrations:

### Core Features
- Environment-based secret loading via `os.environ/NAME`
- Strict YAML parsing via `strict-config` / `CLIPROXY_STRICT_CONFIG`
- Optional encryption-at-rest for `auth-dir` credentials + atomic/locked writes
- Prometheus metrics endpoint (configurable `/metrics`) + optional auth gate (`metrics.require-auth`)
- In-memory response cache (LRU+TTL) for non-streaming JSON endpoints
- Rate limiting (global / per-key parallelism + per-key RPM + per-key TPM)
- Request/response size limits (`limits.max-*-size-mb`)
- Request body guardrail (reject `api_base` / `base_url` by default)
- Virtual keys (managed client keys) + budgets + pricing-based spend tracking
- Fallback chains (`fallback-chains`) + exponential backoff retries (`retry-policy`)
- Pass-through endpoints (`pass-through.endpoints[]`) for forwarding extra routes upstream
- Health endpoints (`/health/liveness`, `/health/readiness`) + optional background probes
- Sensitive-data masking (request logs + redacted management config view)

### Health-Based Routing & Smart Load Balancing

CLIProxyAPIPlus now includes intelligent routing and health tracking based on production-grade proxy patterns:

#### Features

**Health Tracking System**
- Automatic monitoring of credential health based on failure rates and response latency
- Four health status levels: HEALTHY, DEGRADED, COOLDOWN, ERROR
- Rolling window metrics (configurable 60-second default)
- Per-credential and per-model statistics tracking
- P95/P99 latency percentile calculations
- Automatic cooldown integration

**Advanced Routing Strategies**
- **`fill-first`**: Drain one credential before moving to the next (default)
- **`round-robin`**: Sequential credential rotation
- **`random`**: Random credential selection
- **`least-busy`**: Select credential with fewest active requests (load balancing)
- **`lowest-latency`**: Select credential with best P95 latency (performance optimization)

**Health-Aware Routing**
- Automatically filter out COOLDOWN and ERROR credentials
- Prefer HEALTHY credentials over DEGRADED when `prefer-healthy: true`
- Graceful fallback to all credentials when no healthy ones available

#### Configuration Example

```yaml
# Health tracking configuration
health-tracking:
  enable: true
  window-seconds: 60              # Rolling window for failure rate calculation
  failure-threshold: 0.5          # 50% failure rate triggers ERROR status
  degraded-threshold: 0.1         # 10% failure rate triggers DEGRADED status
  min-requests: 5                 # Minimum requests before tracking
  cleanup-interval: 300           # Cleanup old data every 5 minutes

# Enhanced routing configuration
routing:
  strategy: "least-busy"          # fill-first, round-robin, random, least-busy, lowest-latency
  health-aware: true              # Filter unhealthy credentials (COOLDOWN, ERROR)
  prefer-healthy: true            # Prioritize HEALTHY over DEGRADED credentials
```

#### Routing Strategy Comparison

| Strategy | Best For | How It Works |
|----------|----------|--------------|
| `fill-first` | Staggering rolling caps | Uses the first available credential (by ID) until it cools down |
| `round-robin` | Even distribution, predictable | Cycles through credentials sequentially |
| `random` | Simple load balancing | Randomly selects from available credentials |
| `least-busy` | Optimal load distribution | Selects credential with fewest active requests |
| `lowest-latency` | Performance-critical apps | Selects credential with best P95 latency |

#### Health Status Levels

- **HEALTHY**: Normal operation, low failure rates
- **DEGRADED**: Elevated failure rates (above degraded-threshold but below failure-threshold)
- **COOLDOWN**: Temporarily unavailable due to errors or rate limits
- **ERROR**: High failure rates (above failure-threshold) or persistent errors

#### Benefits

- **Improved reliability** by avoiding unhealthy credentials when `health-aware` routing is enabled
- **Better tail latency** when `lowest-latency` is enabled and health tracking has enough data
- **Smarter load balancing** with `least-busy` using in-flight request counts
- **Automatic recovery** from cooldown windows as health improves

See:
- `docs/operations.md`

### Future work

These are high-value ideas that remain on the roadmap:
- OpenTelemetry tracing + external integrations (Langfuse/Sentry/webhooks)
- Redis-backed distributed cache/rate limits for multi-instance deployments
- DB-backed virtual key store + async spend log writer
- Broader endpoint coverage via native translators (beyond pass-through)

## Differences from the Mainline

- Added GitHub Copilot support (OAuth login), provided by [em4go](https://github.com/em4go/CLIProxyAPI/tree/feature/github-copilot-auth)
- Added Kiro (AWS CodeWhisperer) support (OAuth login), provided by [fuko2935](https://github.com/fuko2935/CLIProxyAPI/tree/feature/kiro-integration), [Ravens2121](https://github.com/Ravens2121/CLIProxyAPIPlus/)

## Contributing

This project only accepts pull requests that relate to third-party provider support. Any pull requests unrelated to third-party provider support will be rejected.

If you need to submit any non-third-party provider changes, please open them against the mainline repository.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
