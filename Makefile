ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
export PATH := $(HOME)/go/bin:$(PATH)

install_gow:
	@command -v gow @>/dev/null || \
	go install github.com/mitranim/gow@latest

dev: install_gow
	@PREFIX=$$(printf "—%.0s" $$(seq 1 $$(tput cols))) && \
	gow -S "$${PREFIX}" -i demo run . $(ARGS)
