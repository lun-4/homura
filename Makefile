.PHONY: all build 9p clean install

all: build 9p

build:
	go build -o homura ./cmd/homura

9p:
	cd 9passthrough && go build -o 9passthrough .

test:
	cd 9passthrough && go test -v .

clean:
	rm -f homura 9passthrough/9passthrough
	rm -rf ~/.cache/homura/v3/

install: all
	cp homura ~/.local/bin/
	cp 9passthrough/9passthrough ~/.local/bin/

.DEFAULT_GOAL := all
