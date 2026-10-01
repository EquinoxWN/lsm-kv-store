.PHONY: setup lint test bench fuzz ci audit

setup:
	go mod download

lint:
	@files="$$(gofmt -l $$(go list -f '{{$$d := .Dir}}{{range .GoFiles}}{{$$d}}/{{.}} {{end}}{{range .TestGoFiles}}{{$$d}}/{{.}} {{end}}{{range .XTestGoFiles}}{{$$d}}/{{.}} {{end}}' ./...))"; test -z "$$files" || (echo "$$files"; echo "run gofmt -w"; exit 1)
	go vet ./...

test:
	go test -race -count=1 ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

fuzz:
	go test -run '^$$' -fuzz FuzzReplay -fuzztime 30s ./internal/wal

# Known vulnerabilities in the code paths actually called (needs Go 1.26+, as in CI).
audit:
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

ci: setup lint test
