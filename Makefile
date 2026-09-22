PROTO_DIR := proto/liapoldus/plugin/v1
TS_PLUGIN_PATH := tests/node_modules/.bin/protoc-gen-ts_proto

.PHONY: generate generate-go generate-ts check-generated

generate: generate-go generate-ts

generate-go:
	protoc -I proto \
		--go_out=. --go_opt=module=github.com/Liapoldus/pluginprotocol \
		--go-grpc_out=. --go-grpc_opt=module=github.com/Liapoldus/pluginprotocol \
		liapoldus/plugin/v1/control.proto liapoldus/plugin/v1/service.proto

generate-ts:
	protoc -I proto \
		--plugin=protoc-gen-ts_proto=$(TS_PLUGIN_PATH) \
		--ts_proto_out=tests/generated \
		--ts_proto_opt=outputServices=grpc-js,esModuleInterop=true,importSuffix=.js \
		liapoldus/plugin/v1/control.proto liapoldus/plugin/v1/service.proto

check-generated: generate
	git diff --exit-code -- pluginv1 tests/generated
