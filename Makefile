GOBIN := $(shell go env GOPATH)/bin

.PHONY: build test lint e2e check test-install devserver devserver-down devserver-reset tools

build:
	go build -o simpleota ./cmd/simpleota

test:
	go test -race ./...

lint:
	go vet ./...
	go vet -tags e2e ./e2e/
	$(GOBIN)/staticcheck ./...
	$(GOBIN)/staticcheck -tags e2e ./e2e/
	$(GOBIN)/gosec -quiet -exclude-dir=internal/fakeapi ./...
	$(GOBIN)/govulncheck ./...
	shellcheck -s sh install.sh
	shellcheck scripts/*.sh

# Runs the end-to-end suite against the local dev server, starting it first.
e2e: devserver
	go test -tags e2e -count=1 ./e2e/

check: test lint e2e

# Builds snapshot release archives and installs them with install.sh.
test-install:
	goreleaser release --snapshot --clean --skip=publish
	scripts/test-install.sh

devserver:
	scripts/devserver.sh up

devserver-down:
	scripts/devserver.sh down

devserver-reset:
	scripts/devserver.sh reset

tools:
	go install honnef.co/go/tools/cmd/staticcheck@v0.8.2
	go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
	go install github.com/securego/gosec/v2/cmd/gosec@v2.30.0
