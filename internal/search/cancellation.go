package search

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// reloadCancelCtx is cancelled when a short-lived interactive reload/preview
// process receives a termination signal. ripgrep invocations run under this
// context (exec.CommandContext), so a catchable signal or explicit context
// cancellation also stops the in-flight rg child. Normal fzf supersession
// uses Unix process-group termination or Windows job ownership instead.
//
// It defaults to context.Background() (never cancelled), so normal,
// non-interactive runs are completely unaffected: exec.CommandContext with a
// Background context behaves exactly like exec.Command. Only internal
// reload/preview commands arm it, via InstallReloadCancellation.
var reloadCancelCtx context.Context = context.Background()

// InstallReloadCancellation arms reloadCancelCtx for the lifetime of an
// internal reload/preview process. Catching SIGTERM or SIGINT cancels the
// context and kills rg children started under it. This is cooperative cleanup
// for catchable signals, not the ownership mechanism for fzf's hard kills.
//
// The returned stop func is intentionally discarded: these are ephemeral
// fzf-spawned helper processes, so the registration lives for the whole
// (short) process lifetime.
func InstallReloadCancellation() {
	reloadCancelCtx, _ = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// ReloadWasCancelled reports whether the reload context has been cancelled, so
// rg helpers can treat a kill as a clean empty result (no error, quiet exit)
// rather than surfacing "signal: killed" on a superseded keystroke.
func ReloadWasCancelled() bool {
	return reloadCancelCtx.Err() != nil
}

// ReloadCancelContext exposes the reload-cancellation context so non-rg
// callers (e.g. multi-file preview writers) can honor cancellation too.
// The returned context is cancelled when the current preview
// child receives SIGTERM/SIGINT (after InstallReloadCancellation has
// armed it). Outside an internal preview process this returns the
// never-cancelled background context.
func ReloadCancelContext() context.Context {
	return reloadCancelCtx
}
