package command

// ScopesPayloadKind selects the dedicated metadata-only transport fast path.
// The flag itself belongs to each scope; mixed commands use sectioned output.
func ScopesPayloadKind(scopes []ExecutionScope) PayloadKind {
	if len(scopes) == 0 {
		return PayloadContent
	}
	for _, scope := range scopes {
		if !scope.Metadata {
			return PayloadContent
		}
	}
	return PayloadMetadata
}
