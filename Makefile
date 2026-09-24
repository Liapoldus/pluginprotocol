PROTO_DIR := proto/liapoldus/plugin/v1
TS_PLUGIN_PATH := tests/node_modules/.bin/protoc-gen-ts_proto

.PHONY: check generate generate-go generate-ts check-generated

check: check-generated
	npm test --prefix tests
	go vet ./...
	go test -race ./...
	go build ./...

generate: generate-go generate-ts

generate-go:
	protoc -I proto \
		--go_out=. --go_opt=module=github.com/Liapoldus/pluginprotocol \
		--go-grpc_out=. --go-grpc_opt=module=github.com/Liapoldus/pluginprotocol \
		liapoldus/plugin/v1/control.proto liapoldus/plugin/v1/grant.proto liapoldus/plugin/v1/service.proto

generate-ts:
	protoc -I proto \
		--plugin=protoc-gen-ts_proto=$(TS_PLUGIN_PATH) \
		--ts_proto_out=tests/generated \
		--ts_proto_opt=outputServices=grpc-js,esModuleInterop=true,importSuffix=.js \
		liapoldus/plugin/v1/control.proto liapoldus/plugin/v1/grant.proto liapoldus/plugin/v1/service.proto

check-generated:
	@set -eu; \
		tmp_dir=$$(mktemp -d); \
		trap 'rm -rf "$$tmp_dir"' EXIT HUP INT TERM; \
		mkdir -p "$$tmp_dir/go" "$$tmp_dir/ts"; \
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
			--go_out="$$tmp_dir/go" --go_opt=module=github.com/Liapoldus/pluginprotocol \
			--go-grpc_out="$$tmp_dir/go" --go-grpc_opt=module=github.com/Liapoldus/pluginprotocol \
			liapoldus/plugin/v1/control.proto liapoldus/plugin/v1/grant.proto liapoldus/plugin/v1/service.proto; \
		protoc -I proto \
			--plugin=protoc-gen-ts_proto=$(TS_PLUGIN_PATH) \
			--ts_proto_out="$$tmp_dir/ts" \
			--ts_proto_opt=outputServices=grpc-js,esModuleInterop=true,importSuffix=.js \
			liapoldus/plugin/v1/control.proto liapoldus/plugin/v1/grant.proto liapoldus/plugin/v1/service.proto; \
		compare_generated_tree pluginv1 "$$tmp_dir/go/pluginv1" "$$tmp_dir/go-files"; \
		compare_generated_tree tests/generated "$$tmp_dir/ts" "$$tmp_dir/ts-files"
