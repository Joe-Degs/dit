APPS = tftpd
MAINS = $(addprefix cmd/, $(addsuffix /main.go, $(APPS)))
DEPS = $(shell find . -type f -name '*.go')
BINS = $(addprefix bin/, $(APPS))

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.gitCommit=$(COMMIT)' -X 'main.buildTime=$(BUILD_TIME)'

$(BINS): $(MAINS) $(DEPS)
	go build -race -ldflags "$(LDFLAGS)" -o $@ $<

addcaps: bin/tftpd
	sudo setcap 'cap_net_bind_service=+ep' $<

.PHONY: clean
clean:
	rm -f $(BINS)
