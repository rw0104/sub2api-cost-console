package gateway

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// requestBody bounds both wire bytes and expanded bytes. Official Codex OAuth
// clients send zstd; rejecting every compressed request breaks that client even
// when the same configuration works with an API-key test fixture.
func requestBody(r *http.Request) ([]byte, int, error) {
	return requestBodyWithLimits(r, maxRequestBytes, maxRequestBytes)
}
func requestBodyWithLimits(r *http.Request, limit int64, window uint64) ([]byte, int, error) {
	defer r.Body.Close()
	encoding := strings.ToLower(strings.TrimSpace(strings.Join(r.Header.Values("Content-Encoding"), ",")))
	switch encoding {
	case "", "identity", "gzip", "zstd":
	default:
		return nil, http.StatusUnsupportedMediaType, errors.New("支持未压缩 JSON、gzip 或 zstd；不支持多层编码")
	}
	wire, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if int64(len(wire)) > limit {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("传入请求体超过 %d MiB；请在连接设置中调整请求上限，或缩小上下文", limit>>20)
	}
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("无法完整读取请求体")
	}
	var reader io.Reader = bytes.NewReader(wire)
	switch encoding {
	case "gzip":
		decoder, err := gzip.NewReader(reader)
		if err != nil {
			return nil, http.StatusBadRequest, errors.New("gzip 请求体损坏或不完整")
		}
		defer decoder.Close()
		reader = decoder
	case "zstd":
		decoder, err := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(max(uint64(limit), window)), zstd.WithDecoderMaxWindow(window))
		if err != nil {
			return nil, http.StatusBadRequest, errors.New("zstd 请求体损坏或不完整")
		}
		defer decoder.Close()
		reader = decoder
	default:
		return wire, 0, nil
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(body)) > limit || errors.Is(err, zstd.ErrDecoderSizeExceeded) || errors.Is(err, zstd.ErrWindowSizeExceeded) {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("解压请求体或 zstd 窗口超过限制（请求 %d MiB / 窗口 %d MiB）；请在连接设置调整，正式请求尚未转发", limit>>20, window>>20)
	}
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("压缩请求体损坏或不完整")
	}
	return body, 0, nil
}
