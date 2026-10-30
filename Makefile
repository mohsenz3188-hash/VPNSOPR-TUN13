BINARY_SERVER=vpsopr-server
BINARY_CLIENT=vpsopr-client

.PHONY: build test vet clean
build:
	go build -trimpath -ldflags="-s -w" -o $(BINARY_SERVER) ./cmd/server
	go build -trimpath -ldflags="-s -w" -o $(BINARY_CLIENT) ./cmd/client

test:
	go test -race ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY_SERVER) $(BINARY_CLIENT)
