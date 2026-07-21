.PHONY: build install test vet clean deploy

VERSION ?= v0.2.4
BINARY  ?= pve
LDFLAGS  = -ldflags "-X github.com/tu-usuario/pv/internal/update.CurrentVersion=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/pv/

install: build
	install -m 0755 $(BINARY) /usr/local/bin/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

deploy: build
	scripts/deploy-to-web.sh $(BINARY) $(VERSION)
