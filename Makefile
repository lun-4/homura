.PHONY: all build 9p clean install

all: build 9p

build:
	go build -o homura ./cmd/homura

9p:
	@echo "Building and installing 9p components to ~/.cache/homura/"
	@mkdir -p ~/.cache/homura/bin
	@mkdir -p ~/.cache/homura/src
	cd 9passthrough && go build -o ~/.cache/homura/bin/9passthrough .
	cd 9pvm-request && CGO_ENABLED=0 go build -o ~/.cache/homura/bin/9pvm-request .
	cd guest/9pfuse && CGO_ENABLED=0 go build -o ~/.cache/homura/bin/9pfuse .
	cd guest/9pfuse/cmd/stress && CGO_ENABLED=0 go build -o ~/.cache/homura/bin/9pfuse-stress .
	cd test-fs && CGO_ENABLED=0 go build -o ~/.cache/homura/bin/test-fs .
	@rm -rf ~/.cache/homura/src/9pvm-request
	@rm -rf ~/.cache/homura/src/9pfuse
	@rm -rf ~/.cache/homura/src/test-fs
	@cp -r 9pvm-request ~/.cache/homura/src/
	@cp -r guest/9pfuse ~/.cache/homura/src/
	@cp -r test-fs ~/.cache/homura/src/
	@echo "✓ 9passthrough binary: ~/.cache/homura/bin/9passthrough"
	@echo "✓ 9pvm-request binary: ~/.cache/homura/bin/9pvm-request"
	@echo "✓ 9pfuse binary: ~/.cache/homura/bin/9pfuse"
	@echo "✓ 9pfuse-stress binary: ~/.cache/homura/bin/9pfuse-stress"
	@echo "✓ test-fs binary: ~/.cache/homura/bin/test-fs"
	@echo "✓ 9pvm-request source: ~/.cache/homura/src/9pvm-request/"
	@echo "✓ 9pfuse source: ~/.cache/homura/src/9pfuse/"
	@echo "✓ test-fs source: ~/.cache/homura/src/test-fs/"

test:
	cd 9passthrough && go test -v -count=1 .
	cd guest/9pfuse && go test -v -count=1 .

clean:
	rm -vf homura
	rm -vrf ~/.cache/homura/v4/
	rm -vrf ~/.cache/homura/bin/
	rm -vrf ~/.cache/homura/src/

install: all
	cp homura ~/.local/bin/

.DEFAULT_GOAL := all
