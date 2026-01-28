.PHONY: all build 9p clean install

all: build 9p

build:
	go build -o homura ./cmd/homura

9p:
	@echo "Building and installing 9p components to ~/.cache/homura/"
	@mkdir -p ~/.cache/homura/bin
	@mkdir -p ~/.cache/homura/src
	cd 9passthrough && go build -o ~/.cache/homura/bin/9passthrough .
	cd 9pvm-request && go build -o ~/.cache/homura/bin/9pvm-request .
	@rm -rf ~/.cache/homura/src/9pvm-request
	@cp -r 9pvm-request ~/.cache/homura/src/
	@echo "✓ 9passthrough binary: ~/.cache/homura/bin/9passthrough"
	@echo "✓ 9pvm-request binary: ~/.cache/homura/bin/9pvm-request"
	@echo "✓ 9pvm-request source: ~/.cache/homura/src/9pvm-request/"

test:
	cd 9passthrough && go test -v .

clean:
	rm -f homura
	rm -rf ~/.cache/homura/v4/

install: all
	cp homura ~/.local/bin/

.DEFAULT_GOAL := all
