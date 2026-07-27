package adapter

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/sagernet/sing/common/varbin"
)

func TestSavedBinaryRoundTripUpdateInterval(t *testing.T) {
	t.Parallel()
	expected := SavedBinary{
		Content:        []byte("rule-set"),
		LastUpdated:    time.Unix(1_800_000_000, 0),
		LastEtag:       "etag",
		UpdateInterval: 6 * time.Hour,
	}
	content, err := expected.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var actual SavedBinary
	if err = actual.UnmarshalBinary(content); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Content, expected.Content) ||
		!actual.LastUpdated.Equal(expected.LastUpdated) ||
		actual.LastEtag != expected.LastEtag ||
		actual.UpdateInterval != expected.UpdateInterval {
		t.Fatalf("unexpected round trip: %+v", actual)
	}
}

func TestSavedBinaryReadsVersionOne(t *testing.T) {
	t.Parallel()
	var content bytes.Buffer
	_ = binary.Write(&content, binary.BigEndian, uint8(1))
	_, _ = varbin.WriteUvarint(&content, uint64(len("legacy")))
	_, _ = content.WriteString("legacy")
	_ = binary.Write(&content, binary.BigEndian, int64(1_800_000_000))
	_, _ = varbin.WriteUvarint(&content, uint64(len("etag")))
	_, _ = content.WriteString("etag")

	var actual SavedBinary
	if err := actual.UnmarshalBinary(content.Bytes()); err != nil {
		t.Fatal(err)
	}
	if actual.UpdateInterval != 0 {
		t.Fatalf("legacy entry gained update interval %v", actual.UpdateInterval)
	}
}
