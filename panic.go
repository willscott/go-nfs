package nfs

import (
	"errors"
	"runtime"
)

// ErrAbortHandler is a sentinel panic that suppresses stack logging. Mirrors net/http.ErrAbortHandler.
var ErrAbortHandler = errors.New("nfs: abort handler")

// DefaultPanicHandler logs a stack via Log.Errorf and returns ResponseCodeSystemErr; suppresses logging for ErrAbortHandler.
func DefaultPanicHandler(recovered any) ResponseCode {
	if recovered != ErrAbortHandler {
		const size = 64 << 10
		buf := make([]byte, size)
		buf = buf[:runtime.Stack(buf, false)]
		Log.Errorf("nfs: panic in handler: %v\n%s", recovered, buf)
	}
	return ResponseCodeSystemErr
}

type panicAppError struct {
	code ResponseCode
}

func (p *panicAppError) Code() ResponseCode             { return p.code }
func (p *panicAppError) Error() string                  { return "nfs: handler panic" }
func (p *panicAppError) MarshalBinary() ([]byte, error) { return nil, nil }
