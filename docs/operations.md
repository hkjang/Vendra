# Operations

## Health and logs

- `/health/live`: process liveness
- `/health/ready`: PostgreSQL readiness
- `/metrics`: Prometheus exposition (build, HTTP 요청/오류/지연, PostgreSQL pool)
- stdout: structured JSON request/application logs with request ID
- Docker `HEALTHCHECK`: readiness endpoint every 30 seconds

## Database migration

Embedded, ordered SQL migrations run inside a PostgreSQL transaction at startup. `schema_migrations` prevents reapplication. Back up before upgrading and never run two different Vendra versions against the same schema during migration.

## Release and rollback

Load the target archive and update the exact tag in `compose.yaml`. For rollback, restore the matching PostgreSQL/document backup if a newer migration is not backward compatible.

```bash
docker load < vendra-v0.1.0.tar.gz
docker compose up -d
docker compose ps
curl -fsS http://localhost:8080/health/ready
```

## Offline prerequisites

Transfer the Vendra archive and, if the target network does not already have them, an approved PostgreSQL image or PostgreSQL installation media. Vendra itself makes no required internet calls. OIDC, webhook and AI endpoints may point to services inside the offline network.
