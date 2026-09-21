//go:build !recoveryfault

package recovery

func buildFaultHook() func(string, int) error { return nil }
