# Token Monitor Community Relay

The relay accepts anonymous numeric usage reports from Token Monitor clients and writes them to the public GitCode `community-data` branch. The GitCode token exists only on the VPS.

## Runtime

- Listen address: `127.0.0.1:18190`
- Public prefix: `https://new.taqi.cc/token-monitor-community/`
- Health check: `GET /health`
- Report endpoint: `POST /v1/report`

Required environment variables:

```text
GITCODE_TOKEN=...
LISTEN_ADDR=127.0.0.1:18190
COMMUNITY_BRANCH=community-data
```

Deployment templates are in `deploy/`. Store the environment in `/etc/token-monitor-community.env` with mode `0600`, install the binary at `/usr/local/bin/token-monitor-community-relay`, then enable `token-monitor-community.service`. Nginx rate limiting is defined at HTTP scope by `nginx-limit.conf`; `nginx-location.conf` belongs in the HTTPS server block before the catch-all location.

## Verification

```bash
go test ./...
curl -fsS https://new.taqi.cc/token-monitor-community/health
```

Before an application release, also verify two independent identities can create and update reports, a wrong device secret receives HTTP 403, and the reports can be read back from GitCode.
