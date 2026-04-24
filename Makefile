.PHONY: build run test clean

build:
	go build -o eva .

run: build
	./eva -i

test:
	go test ./...

clean:
	rm -f eva