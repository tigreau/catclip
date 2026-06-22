package search

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// reloadCancelCtx is cancelled when a short-lived interactive reload/preview
// process receives a termination signal. ripgrep invocations run under this
// context (exec.CommandContext), so when fzf terminates a superseded reload
// command — e.g. the user typed another character before the previous content
// scan finished — the in-flight rg child is killed too, instead of being
// orphaned to scan the whole corpus to completion.
//
// It defaults to context.Background() (never cancelled), so normal,
// non-interactive runs are completely unaffected: exec.CommandContext with a
// Background context behaves exactly like exec.Command. Only internal
// reload/preview commands arm it, via InstallReloadCancellation.
var reloadCancelCtx context.Context = context.Background()

// InstallReloadCancellation arms reloadCancelCtx for the lifetime of an
// internal reload/preview process. fzf sends SIGTERM to the previous reload
// command when a new keystroke triggers a fresh one; catching it cancels
// reloadCancelCtx, which kills the rg children started under it. SIGINT covers
// manual aborts. On platforms where fzf does not send SIGTERM (Windows), this
// simply never fires and rg runs to completion as before — no regression.
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
// callers (e.g. multi-file preview writers) can honor fzf's supersede
// signal too. The returned context is cancelled when the current preview
// child receives SIGTERM/SIGINT (after InstallReloadCancellation has
// armed it). Outside an internal preview process this returns the
// never-cancelled background context.
func ReloadCancelContext() context.Context {
	return reloadCancelCtx
}
