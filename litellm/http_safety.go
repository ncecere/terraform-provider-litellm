package litellm

import (
	"errors"
	"io"
)

const maxLegacyResponseBody = int64(128 << 20) // 128 MiB

var errLegacyResponseTooLarge = errors.New("LiteLLM response exceeded the provider safety limit")

// readLegacyResponseBody bounds the compiled legacy package while it remains
// in the repository. The active provider uses internal/provider instead.
func readLegacyResponseBody(reader io.Reader) ([]byte, error) {
	return readLegacyResponseBodyLimit(reader, maxLegacyResponseBody)
}

func readLegacyResponseBodyLimit(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, errors.New("failed to read LiteLLM response")
	}
	if int64(len(body)) > limit {
		return nil, errLegacyResponseTooLarge
	}
	return body, nil
}
