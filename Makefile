ifeq ($(origin ARGS), undefined)
export ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
endif

BUILD_DIR ?= "."
export PATH := $(HOME)/go/bin:$(PATH)

install_gow:
	@command -v gow @>/dev/null || \
	go install github.com/mitranim/gow@latest

install-virtualenv:
	@command -v virtualenv @>/dev/null || \
	python -m pip install virtualenv

gow: install-gow
	@PREFIX=$$(printf "—%.0s" $$(seq 1 $$(tput cols))) && \
	gow -S "$${PREFIX}" -i "$(EXCLUDE_DIR)" -w "$(BUILD_DIR)" run "$(BUILD_DIR)" $(ARGS)

dev:
	@$(MAKE) gow EXCLUDE_DIR="demo"

demo-%:
	@$(MAKE) gow BUILD_DIR="./demo/$*"

demo-signaling-api: install-virtualenv
	@cd demo/signaling-api && \
	virtualenv venv -q && \
	source venv/bin/activate && \
	python -m pip install -qr requirements.txt && \
	python main.py ; \
	deactivate

%:
	@:
