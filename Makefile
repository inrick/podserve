.PHONY: all debug css install-freebsd uninstall-freebsd

all:
	go build

debug:
	gdlv debug .

css:
	tailwindcss -c web/tailwind.config.js -i web/input.css -o static/style.css --minify --watch

install-freebsd: all
	./deployment/freebsd/install.sh

uninstall-freebsd:
	./deployment/freebsd/uninstall.sh
