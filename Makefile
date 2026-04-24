.PHONY: build run test clean dev-reload

build:
	go build -o eva .

run: build
	./eva -i

test:
	go test ./...

clean:
	rm -f eva

dev-reload:
	cd /home/j && air -c .air.toml
