//go:build windows

package sys

const (
	ES_CONTINUOUS       uint32 = 0x80000000
	ES_DISPLAY_REQUIRED uint32 = 0x00000002
)

//sys SetThreadExecutionState(esFlags uint32) (ret uint32) = kernel32.SetThreadExecutionState
