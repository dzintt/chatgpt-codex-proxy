package codex

import (
	"fmt"
	"io"
)

const upstreamErrorBodyLimit int64 = 32 * 1024

func readLimitedErrorBody(r io.Reader) string {
	if r == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(r, upstreamErrorBodyLimit))
	if err != nil {
		return fmt.Sprintf("<failed to read upstream response body: %v>", err)
	}
	return string(body)
}

func drainLimitedBody(r io.Reader) error {
	if r == nil {
		return nil
	}
	_, err := io.Copy(io.Discard, io.LimitReader(r, upstreamErrorBodyLimit))
	return err
}
