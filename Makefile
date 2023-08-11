ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
export PATH := $(HOME)/go/bin:$(PATH)

install_gow:
	@command -v gow @>/dev/null || \
	go install github.com/mitranim/gow@latest

dev:
	@PREFIX=$$(printf '%0.s—' {1..80}) && \
	gow -S "$${PREFIX}" run . $(ARGS)
