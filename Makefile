MODULE := github.com/Liapoldus/pluginprotocol
# The generic peer wire contract. It is generated for Go only: it has no service
# definitions, so it needs no gRPC stubs, and the TypeScript side drives real Go
# child processes instead of speaking the wire directly.
PEER_PROTOS := liapoldus/peer/v1/peer.proto
PEER_WIRE_DIR := infrastructure/peer/wire

.PHONY: check check-race generate generate-go check-generated

check: check-generated
	npm test --prefix tests
	go vet ./...
	go build -o /dev/null ./...

# The conformance suite, with every Go fixture built under the race detector. The
# same scenarios run, so a data race in the transport or connection engine fails a
# behavioural assertion instead of passing silently under the default build.
check-race: check-generated
	LIAPOLDUS_PEER_FIXTURE_GOFLAGS=-race npm test --prefix tests

generate: generate-go

generate-go:
	protoc -I proto \
		--go_out=. --go_opt=module=$(MODULE) \
		$(PEER_PROTOS)

check-generated:
	@set -eu; \
		tmp_dir=$$(mktemp -d); \
		trap 'rm -rf "$$tmp_dir"' EXIT HUP INT TERM; \
		mkdir -p "$$tmp_dir/go"; \
		compare_generated_tree() { \
			source_dir="$$1"; generated_dir="$$2"; file_list="$$3"; \
			(cd "$$source_dir" && find . -type f -print | LC_ALL=C sort) > "$$file_list.source"; \
			(cd "$$generated_dir" && find . -type f -print | LC_ALL=C sort) > "$$file_list.generated"; \
			diff -u "$$file_list.source" "$$file_list.generated"; \
			while IFS= read -r relative_file; do \
				cmp "$$source_dir/$$relative_file" "$$generated_dir/$$relative_file"; \
			done < "$$file_list.source"; \
		}; \
		protoc -I proto \
			--go_out="$$tmp_dir/go" --go_opt=module=$(MODULE) \
			$(PEER_PROTOS); \
		compare_generated_tree $(PEER_WIRE_DIR) "$$tmp_dir/go/$(PEER_WIRE_DIR)" "$$tmp_dir/peer-wire-files"
