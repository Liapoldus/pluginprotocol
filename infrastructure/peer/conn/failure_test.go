package conn

import (
	"errors"
	"strconv"
	"testing"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/codec"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/wire"
)

func TestFailureStatusCannotWrapIntoSuccess(t *testing.T) {
	for _, code := range []uint32{256, 257, 1 << 31, 1<<32 - 1} {
		t.Run(stringCode(code), func(t *testing.T) {
			body, err := codec.Marshal(&wire.Failure{Status: &wire.Status{Code: code}})
			if err != nil {
				t.Fatal(err)
			}
			failure, err := decodeFailure(body)
			if !errors.Is(err, peer.ErrProtocolViolation) || failure != nil {
				t.Fatalf("out-of-range code accepted: failure=%v error=%v", failure, err)
			}
		})
	}
}

func stringCode(code uint32) string { return strconv.FormatUint(uint64(code), 10) }
