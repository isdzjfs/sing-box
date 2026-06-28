package proxyprovider

import (
	"io"
	"strconv"

	E "github.com/sagernet/sing/common/exceptions"
)

func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, E.New("content exceeds size limit")
	}
	return content, nil
}

func stringIndex(index int) string {
	return strconv.Itoa(index)
}
