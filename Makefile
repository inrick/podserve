.PHONY: all debug css

all:
	go build

debug:
	gdlv debug .

css:
	tailwindcss -c web/tailwind.config.js -i web/input.css -o static/style.css --minify --watch
