package main

import (
	"bytes"
	"testing"
)

func TestSafeDescriptorsRejectsGeneratorDrift(t *testing.T) {
	source := []byte(`package wire
import (
	unsafe "unsafe"
)
const file_liapoldus_peer_v1_peer_proto_rawDesc = "descriptor"
var a = unsafe.Slice(unsafe.StringData(file_liapoldus_peer_v1_peer_proto_rawDesc), len(file_liapoldus_peer_v1_peer_proto_rawDesc))
var b = unsafe.Slice(unsafe.StringData(file_liapoldus_peer_v1_peer_proto_rawDesc), len(file_liapoldus_peer_v1_peer_proto_rawDesc))
`)
	result, err := safeDescriptors(source)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result, []byte("unsafe")) || bytes.Count(result, []byte("[]byte(file_liapoldus_peer_v1_peer_proto_rawDesc)")) != 2 {
		t.Fatal("descriptor views were not replaced with copies")
	}
	if _, err := safeDescriptors(result); err == nil {
		t.Fatal("accepted unexpected generator output")
	}
	drift := append(bytes.Clone(source), []byte("var c = unsafe.Pointer(nil)\n")...)
	if _, err := safeDescriptors(drift); err == nil {
		t.Fatal("accepted additional unsafe code")
	}
}
