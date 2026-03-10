# Getting Started

## Quick Start (Docker Compose)

```bash
docker compose up --build
```

Endpoints:
- Master: `http://localhost:9333`
- Volume: `http://localhost:8080`
- Filer: `http://localhost:8888`
- S3: `http://localhost:8333`

## Build From Source

```bash
go build -o blob ./goblob
```

## First Upload/Download

```bash
# allocate manually for demo via known volume id+needle id cookie
curl -X PUT "http://localhost:8080/1,000000000000000100000001" -d "hello"
curl "http://localhost:8080/1,000000000000000100000001"
```
