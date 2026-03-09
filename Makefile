.PHONY: build test lint proto clean

build:
	go build -o blob ./goblob/

test:
	go test ./goblob/... -race -count=1

lint:
	golangci-lint run ./goblob/...

proto:
	find proto/ -name '*.proto' | xargs protoc \
	  --go_out=. --go_opt=paths=import \
	  --go-grpc_out=. --go-grpc_opt=paths=import \
	  -I proto/

clean:
	rm -f blob
	find . -name '*.pb.go' -delete
