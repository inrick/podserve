.PHONY: all debug

all:
	go build

debug:
	gdlv debug .
