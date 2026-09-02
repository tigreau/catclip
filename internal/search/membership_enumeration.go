package search

import (
	"strconv"
	"sync"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

// MembershipEnumerationKind identifies a subprocess that can discover path
// membership. Content scans over an explicit retained path list are not
// membership enumeration and intentionally do not use this type.
type MembershipEnumerationKind string

const (
	MembershipEnumerationFiles       MembershipEnumerationKind = "rg-files"
	MembershipEnumerationVisibleSet  MembershipEnumerationKind = "visible-set"
	MembershipEnumerationIgnoreDebug MembershipEnumerationKind = "rg-debug-ignore"
)

// MembershipEnumerationAuthority distinguishes a path-producing subprocess
// that may define selected target membership from a diagnostic inventory that
// is forbidden from changing it. The zero value preserves the ordinary target
// membership contract for every existing caller.
type MembershipEnumerationAuthority uint8

const (
	MembershipAuthorityTarget MembershipEnumerationAuthority = iota
	MembershipAuthorityDiagnostic
)

// MembershipIgnorePolicy describes which ignore universe an enumeration can
// observe.
type MembershipIgnorePolicy string

const (
	MembershipVisible  MembershipIgnorePolicy = "visible"
	MembershipNoIgnore MembershipIgnorePolicy = "no-ignore"
)

// MembershipEnumerationReason is a bounded, path-free explanation for an
// authorized membership subprocess.
type MembershipEnumerationReason string

const (
	MembershipReasonUnspecified         MembershipEnumerationReason = "unspecified"
	MembershipReasonPrimaryTarget       MembershipEnumerationReason = "primary-target-generation"
	MembershipReasonNoIgnoreExpansion   MembershipEnumerationReason = "no-ignore-expansion"
	MembershipReasonIgnoreAttribution   MembershipEnumerationReason = "ignore-attribution"
	MembershipReasonCanonicalFallback   MembershipEnumerationReason = "canonical-fallback"
	MembershipReasonIgnoredAncestor     MembershipEnumerationReason = "ignored-ancestor-diagnostic"
	MembershipReasonTargetInventory     MembershipEnumerationReason = "target-inventory"
	MembershipReasonTextSetFallback     MembershipEnumerationReason = "text-set-fallback"
	MembershipReasonIgnoreRuleListing   MembershipEnumerationReason = "ignore-rule-listing"
	MembershipReasonMetadataIgnoreTrace MembershipEnumerationReason = "metadata-ignore-trace"
	MembershipReasonBasenameResolution  MembershipEnumerationReason = "basename-resolution"
)

// MembershipEnumerationContext carries only structural lifecycle identity.
// It must never contain working directories, targets, patterns, or other user
// path data. ScopeKnown distinguishes scope zero from a non-scope operation.
type MembershipEnumerationContext struct {
	Reason       MembershipEnumerationReason
	ScopeIndex   int
	ScopeKnown   bool
	GenerationID uint64
	Authority    MembershipEnumerationAuthority
}

// WithReason retains lifecycle identity while assigning the reason owned by a
// specific enumeration boundary.
func (c MembershipEnumerationContext) WithReason(reason MembershipEnumerationReason) MembershipEnumerationContext {
	c.Reason = reason
	return c
}

// MembershipEnumerationEvent is emitted once after an actual membership
// subprocess attempt completes. It is intentionally path-free.
type MembershipEnumerationEvent struct {
	Kind         MembershipEnumerationKind
	IgnorePolicy MembershipIgnorePolicy
	Context      MembershipEnumerationContext
	Results      int
	Failed       bool
	Cancelled    bool
	Duration     time.Duration
}

var membershipEnumerationObserver struct {
	sync.RWMutex
	fn func(MembershipEnumerationEvent)
}

// SetMembershipEnumerationObserver installs a process-local engineering/test
// observer and returns a restoration closure. Tests using it must not run in
// parallel with other observer-owning tests.
func SetMembershipEnumerationObserver(observer func(MembershipEnumerationEvent)) func() {
	membershipEnumerationObserver.Lock()
	previous := membershipEnumerationObserver.fn
	membershipEnumerationObserver.fn = observer
	membershipEnumerationObserver.Unlock()
	return func() {
		membershipEnumerationObserver.Lock()
		membershipEnumerationObserver.fn = previous
		membershipEnumerationObserver.Unlock()
	}
}

type membershipEnumerationSpan struct {
	kind         MembershipEnumerationKind
	ignorePolicy MembershipIgnorePolicy
	context      MembershipEnumerationContext
	started      time.Time
}

func beginMembershipEnumeration(kind MembershipEnumerationKind, policy MembershipIgnorePolicy, context MembershipEnumerationContext) *membershipEnumerationSpan {
	membershipEnumerationObserver.RLock()
	hasObserver := membershipEnumerationObserver.fn != nil
	membershipEnumerationObserver.RUnlock()
	if !hasObserver && !platform.InternalBenchEnabled() {
		return nil
	}
	if context.Reason == "" {
		context.Reason = MembershipReasonUnspecified
	}
	return &membershipEnumerationSpan{
		kind:         kind,
		ignorePolicy: policy,
		context:      context,
		started:      time.Now(),
	}
}

func (s *membershipEnumerationSpan) finish(results int, cancelled bool, err error) {
	if s == nil {
		return
	}
	event := MembershipEnumerationEvent{
		Kind:         s.kind,
		IgnorePolicy: s.ignorePolicy,
		Context:      s.context,
		Results:      results,
		Failed:       err != nil,
		Cancelled:    cancelled,
		Duration:     time.Since(s.started),
	}
	membershipEnumerationObserver.RLock()
	observer := membershipEnumerationObserver.fn
	membershipEnumerationObserver.RUnlock()
	if observer != nil {
		observer(event)
	}
	platform.InternalBenchLog("search.membership_enumeration",
		"scan_class", "membership",
		"membership_authority", strconv.FormatBool(event.Context.Authority == MembershipAuthorityTarget),
		"kind", string(event.Kind),
		"ignore_policy", string(event.IgnorePolicy),
		"reason", string(event.Context.Reason),
		"scope_known", strconv.FormatBool(event.Context.ScopeKnown),
		"scope_index", strconv.Itoa(event.Context.ScopeIndex),
		"generation_id", strconv.FormatUint(event.Context.GenerationID, 10),
		"results", strconv.Itoa(event.Results),
		"failed", strconv.FormatBool(event.Failed),
		"cancelled", strconv.FormatBool(event.Cancelled),
		"elapsed", event.Duration.String(),
	)
}

func membershipContextOrDefault(contexts []MembershipEnumerationContext, reason MembershipEnumerationReason) MembershipEnumerationContext {
	if len(contexts) == 0 {
		return MembershipEnumerationContext{Reason: reason}
	}
	context := contexts[0]
	if context.Reason == "" {
		context.Reason = reason
	}
	return context
}
