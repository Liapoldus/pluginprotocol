package wire

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestDescriptorAndOpaquePayloadRoundTrip(t *testing.T) {
	input := &CallRequest{Method: "consumer.method", Payload: []byte{0, 255, 17}}
	descriptor := input.ProtoReflect().Descriptor()
	if descriptor.FullName() != "liapoldus.peer.v1.CallRequest" || descriptor.Fields().ByName("method").Number() != 1 || descriptor.Fields().ByName("payload").Number() != 2 {
		t.Fatal("wire descriptor changed")
	}
	encoded, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output CallRequest
	if err := proto.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(input, &output) {
		t.Fatal("wire payload changed")
	}
}
