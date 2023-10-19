APPS = tftpd
# MAINS = $(addprefix cmd/,$(addsuffix /main.go, $(APPS)))
BINS = $(addprefix bin/, $(APPS))

.PHONY: all $(APPS) # install clean

all: $(APPS)

$(APPS):
	go build -race -o bin/$@ cmd/$@/main.go


clean:
	rm -f $(BINS)
