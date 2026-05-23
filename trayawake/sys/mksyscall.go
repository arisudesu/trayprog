//go:build generate

package sys

//go:generate go tool mkwinsyscall -output zsyscall_windows.go syscall.go
