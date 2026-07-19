# switch-cli-exporter

Minimal Prometheus exporter for managed switches that expose CPU utilization only through an interactive SSH console. It was created for the MokerLink `2G080610GSM` CLI, whose `show cpu utilization` command returns `Current: N%`.

The exporter is a static, non-root binary. It periodically opens a PTY, completes the switch's application-level login, caches the result, and serves it on `/metrics`. SSH host-key pinning is mandatory.

## Configuration

| Environment variable | Required | Default |
|---|---:|---|
| `SWITCH_HOST` | yes | — |
| `SWITCH_USERNAME` | yes | — |
| `SWITCH_PASSWORD` | yes | — |
| `SWITCH_HOST_KEY_SHA256` | yes | — |
| `SWITCH_PORT` | no | `22` |
| `SWITCH_DEVICE` | no | `switch` |
| `SWITCH_CPU_COMMAND` | no | `show cpu utilization` |
| `SCRAPE_INTERVAL` | no | `30s` |
| `SCRAPE_TIMEOUT` | no | `15s` |
| `LISTEN_ADDR` | no | `:9808` |

Metrics:

- `switch_cpu_utilization_percent`
- `switch_cli_scrape_success`
- `switch_cli_scrape_duration_seconds`
- `switch_cli_last_success_timestamp_seconds`

The password is never included in metrics or log messages.

## Container

```text
ghcr.io/segator/switch-cli-exporter:v0.1.1
```
