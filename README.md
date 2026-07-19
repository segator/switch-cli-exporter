# switch-cli-exporter

Minimal Prometheus exporter for managed switches whose web UI exposes CPU and memory through a JSON API. It was created for the MokerLink `2G080610GSM`, whose `sys_cpumem` endpoint returns `cpu` and `mem` percentages.

The exporter is a static, non-root binary. It authenticates to the switch web API using the same RSA challenge used by the UI, polls `sys_cpumem`, caches the result, and serves it on `/metrics`.

## Configuration

| Environment variable | Required | Default |
|---|---:|---|
| `SWITCH_HOST` | yes | — |
| `SWITCH_USERNAME` | yes | — |
| `SWITCH_PASSWORD` | yes | — |
| `SWITCH_DEVICE` | no | `switch` |
| `SCRAPE_INTERVAL` | no | `60s` |
| `SCRAPE_TIMEOUT` | no | `10s` |
| `LISTEN_ADDR` | no | `:9808` |

Metrics:

- `switch_cpu_utilization_percent`
- `switch_memory_used_percent`
- `switch_cli_scrape_success`
- `switch_cli_scrape_duration_seconds`
- `switch_cli_last_success_timestamp_seconds`

The password is never included in metrics or log messages.

## Container

```text
ghcr.io/segator/switch-cli-exporter:v0.2.0
```
