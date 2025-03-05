APPS = tftpd
MAINS = $(addprefix cmd/, $(addsuffix /main.go, $(APPS)))
DEPS = $(shell find . -type f -name '*.go')
BINS = $(addprefix bin/, $(APPS))

$(BINS): $(MAINS) $(DEPS)
	go build -race -o $@ $<

addcaps: bin/tftpd
	sudo setcap 'cap_net_bind_service=+ep' $<

.PHONY: clean
clean:
	rm -f $(BINS)
