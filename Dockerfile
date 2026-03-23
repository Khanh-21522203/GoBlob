# syntax=docker/dockerfile:1

FROM golang:1.24 AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o /blob ./goblob

FROM debian:bookworm-slim
COPY --from=builder /blob /blob
EXPOSE 8333 8334 8888 9090
ENTRYPOINT ["/blob"]
