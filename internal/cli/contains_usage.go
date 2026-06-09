package cli

func ContainsMissingPatternError(args []string, containsIndex int) error {
	suggestionArgs, ok := suggestContainsSnippetOrdering(args, containsIndex)
	if !ok {
		return ValidationFailure{Reason: ReasonRequiredValue, Flag: "--contains"}
	}
	if _, err := ParseArgsAllowImplicitDot(suggestionArgs); err != nil {
		return ValidationFailure{Reason: ReasonRequiredValue, Flag: "--contains"}
	}
	return ValidationFailure{Reason: ReasonRequiredValue, Flag: "--contains", Suggestion: FormatResolvedStartupCommand(suggestionArgs)}
}

func suggestContainsSnippetOrdering(args []string, containsIndex int) ([]string, bool) {
	if containsIndex+2 >= len(args) {
		return nil, false
	}
	if args[containsIndex+1] != "--snippet" {
		return nil, false
	}
	regex := args[containsIndex+2]
	if IsKnownScopeModifierToken(regex) || regex == "--" || regex == "--then" {
		return nil, false
	}

	suggestion := make([]string, 0, len(args))
	suggestion = append(suggestion, args[:containsIndex]...)
	suggestion = append(suggestion, "--snippet", regex)
	suggestion = append(suggestion, args[containsIndex+3:]...)
	return suggestion, true
}
