package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Liapoldus/pluginprotocol/framing"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "decode-frame" {
		fmt.Fprintln(os.Stderr, "usage: protocol-probe decode-frame HEX")
		os.Exit(2)
	}

	raw, err := hex.DecodeString(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	frame, err := framing.Decode(bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
		"kind":          frame.Kind.String(),
		"requestId":     fmt.Sprint(frame.RequestId),
		"streamId":      fmt.Sprint(frame.StreamId),
		"payloadBase64": base64.StdEncoding.EncodeToString(frame.Payload),
	})
}
