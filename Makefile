BIN := arlo-term
DIR ?= /usr/local/bin

.PHONY: start build install

start: build
	./$(BIN)

build:
	go build -o $(BIN) .

install: build
	./$(BIN) install $(DIR)
