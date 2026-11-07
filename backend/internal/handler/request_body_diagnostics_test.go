package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestdiag"
	"github.com/stretchr/testify/require"
)

type diagnosticsSingleReadBody struct {
	body  []byte
	reads int
	err   error
}

func (r *diagnosticsSingleReadBody) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.body)
	if r.err != nil {
		return n, r.err
	}
	return n, io.EOF
}

func TestRequestBodyDiagnosticsUseNormalizedBytesWithoutReadingAgain(t *testing.T) {
	body := &diagnosticsSingleReadBody{body: []byte("\xef\xbb\xbf{\"input\":\"你好\",\"instructions\":\"line\nbreak\"}")}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	req = req.WithContext(requestdiag.WithContext(context.Background()))
	read, err := readLenientJSONRequestBodyWithPrealloc(req, nil)
	require.NoError(t, err)
	require.Equal(t, 1, body.reads)
	got := requestdiag.Load(req.Context())
	require.True(t, got.RequestObserved)
	require.True(t, got.JSONFieldsObserved)
	require.EqualValues(t, len(read), got.RequestBodyBytes)
	require.EqualValues(t, len(`"你好"`), got.InputBytes)
	require.EqualValues(t, len(`"line\u000abreak"`), got.InstructionsBytes)
}

func TestRequestBodyDiagnosticsDoNotInventSizeForFailedRead(t *testing.T) {
	body := &diagnosticsSingleReadBody{body: []byte(`{"input":`), err: io.ErrUnexpectedEOF}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	req = req.WithContext(requestdiag.WithContext(context.Background()))
	_, err := readLenientJSONRequestBodyWithPrealloc(req, nil)
	require.Error(t, err)
	require.False(t, requestdiag.Load(req.Context()).RequestObserved)
}
