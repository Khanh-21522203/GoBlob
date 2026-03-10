# Docker Deployment

```bash
docker build -t goblob:latest .
docker compose up -d
```

Use `docker compose logs -f` to inspect startup and readiness.
