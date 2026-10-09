// Tool safe-protobuf replaces protoc-gen-go's unsafe descriptor views with owned
// byte copies. It runs on both generation and verification output so wire code
// receives the same security checks as hand-written Go, without G103 exclusions.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/v2/tests/support/fixture"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: safe-protobuf peer.pb.go")
	}
	path := os.Args[1]
	source, err := fixture.ReadFile(path)
	must(err)
	rewritten, err := safeDescriptors(source)
	must(err)
	root, err := os.OpenRoot(filepath.Dir(path))
	must(err)
	defer fixture.Close(root)
	file, err := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_TRUNC, 0600)
	must(err)
	_, err = file.Write(rewritten)
	closeErr := file.Close()
	must(errors.Join(err, closeErr))
}

func safeDescriptors(source []byte) ([]byte, error) {
	// Restrict the rewrite to this generator's immutable descriptor constant;
	// refuse unexpected output instead of silently accepting new unsafe code.
	pattern := regexp.MustCompile(`unsafe\.Slice\(unsafe\.StringData\((file_liapoldus_peer_v1_peer_proto_rawDesc)\), len\(file_liapoldus_peer_v1_peer_proto_rawDesc\)\)`)
	if len(pattern.FindAll(source, -1)) != 2 {
		return nil, fmt.Errorf("unexpected protobuf descriptor conversion count")
	}
	result := pattern.ReplaceAll(source, []byte(`[]byte($1)`))
	if bytes.Contains(result, []byte("unsafe.")) {
		return nil, fmt.Errorf("unexpected unsafe call in protobuf output")
	}
	result = bytes.Replace(result, []byte("\tunsafe \"unsafe\"\n"), nil, 1)
	return format.Source(result)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
