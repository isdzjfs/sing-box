package transport

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadMessageReportsTruncatedPayloadStage(t *testing.T) {
	var framed bytes.Buffer
	require.NoError(t, binary.Write(&framed, binary.BigEndian, uint16(12)))
	_, err := framed.Write(make([]byte, 5))
	require.NoError(t, err)

	_, err = ReadMessage(&framed)

	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.EqualError(t, err, "read DNS message payload (declared_length=12): unexpected EOF")
}

func TestReadMessageReportsUnpackStage(t *testing.T) {
	var framed bytes.Buffer
	require.NoError(t, binary.Write(&framed, binary.BigEndian, uint16(10)))
	_, err := framed.Write(make([]byte, 10))
	require.NoError(t, err)

	_, err = ReadMessage(&framed)

	require.Error(t, err)
	require.Contains(t, err.Error(), "unpack DNS message payload (length=10)")
}
